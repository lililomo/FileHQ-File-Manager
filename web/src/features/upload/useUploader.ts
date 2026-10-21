/** Upload queue (SPEC §8.8): precheck → conflict/rejection dialogs → tus
 *  uploads, 3 files in parallel, pause/resume/cancel, speed and ETA, resume
 *  after a reload through the tus fingerprint. The queue is a module so the
 *  drop handlers, the toolbar, the paste handler and the panel share it; the
 *  hook only hands out stable callbacks. */
import i18next from 'i18next'
import { useMemo } from 'react'
import type { DetailedError, Upload as TusUpload, UploadOptions } from 'tus-js-client'
import { announce } from '@/lib/announce'
import { ApiError } from '@/lib/api'
import type { Conflict, ConflictPolicy } from '@/features/file-ops/api'
import { askConflicts, askUploadPrecheck } from '@/features/file-ops/store'
import { useSessionStore } from '@/stores/session'
import { isPublicScope, publicBase } from '@/lib/public-scope'
import {
  useTransfersStore,
  type Transfer,
  type TransferResult,
  type TransferStatus,
} from '@/stores/transfers'
import { entryKey, precheckUpload, PRECHECK_BATCH, TUS_ENDPOINT, type PrecheckItem } from './api'
import {
  entriesFromClipboard,
  entriesFromDataTransfer,
  entriesFromFileList,
  type UploadEntry,
} from './folder-entries'

export const CONCURRENCY = 3
/** SPEC §6.5 retryDelays. */
export const RETRY_DELAYS = [0, 1000, 3000, 5000, 10000]
const SPEED_WINDOW_MS = 5000
const STORE_THROTTLE_MS = 250

export type UploadPolicy = Exclude<ConflictPolicy, 'fail'>

interface FileRecord {
  file: File
  policy: UploadPolicy
}

interface LiveUpload {
  /** null until the tus module is loaded and the Upload constructed */
  upload: TusUpload | null
  /** set while we abort on purpose (pause/cancel) so onError is ignored */
  silenced: boolean
}

export type UploaderEvent =
  | { type: 'done'; transfer: Transfer }
  | { type: 'error'; transfer: Transfer }
  | { type: 'drained'; done: number; skipped: number; errors: number; dests: string[] }

type Listener = (ev: UploaderEvent) => void

/** Injected tus module (tests) or the lazily imported real one. */
type TusModule = { Upload: typeof TusUpload }
let tusModule: Promise<TusModule> | null = null
export function setTusModule(m: TusModule | null): void {
  tusModule = m ? Promise.resolve(m) : null
}
function loadTus(): Promise<TusModule> {
  if (!tusModule) tusModule = import('tus-js-client').then((m) => ({ Upload: m.Upload }))
  return tusModule
}

type LooseT = (key: string, options?: Record<string, unknown>) => string
const rawT = i18next.t as unknown as LooseT
const t: LooseT = (key, options) => String(options ? rawT(key, options) : rawT(key))

class Uploader {
  private readonly files = new Map<string, FileRecord>()
  private readonly live = new Map<string, LiveUpload>()
  private readonly waiting: string[] = []
  private readonly listeners = new Set<Listener>()
  private readonly samples = new Map<string, { t: number; sent: number }[]>()
  private readonly lastStoreUpdate = new Map<string, number>()
  private batch = { done: 0, skipped: 0, errors: 0, dests: new Set<string>() }
  /** Chunk size from the server config; falls back to 16 MB. */
  chunkSize = 16 * 1024 * 1024

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }

  private emit(ev: UploaderEvent): void {
    for (const fn of this.listeners) fn(ev)
  }

  /** Transfers that can still move (for tests and the panel). */
  liveCount(): number {
    return this.live.size
  }
  waitingCount(): number {
    return this.waiting.length
  }

  // --- planning -----------------------------------------------------------

  /** Precheck + dialogs, then enqueue. Returns the number of transfers started. */
  async plan(entries: UploadEntry[], dest: string): Promise<number> {
    if (entries.length === 0) return 0
    const cfg = useSessionStore.getState().config
    if (cfg?.chunkSize) this.chunkSize = cfg.chunkSize
    const verdicts = new Map<string, PrecheckItem>()
    try {
      for (let i = 0; i < entries.length; i += PRECHECK_BATCH) {
        const slice = entries.slice(i, i + PRECHECK_BATCH)
        const res = await precheckUpload(
          dest,
          slice.map((e) => ({
            name: e.file.name,
            size: e.file.size,
            relativePath: e.relativePath,
          })),
        )
        if (res.chunkSize) this.chunkSize = res.chunkSize
        res.items.forEach((it, idx) => {
          const e = slice[idx]!
          verdicts.set(entryKey(e.relativePath, e.file.name), it)
        })
        if (i + PRECHECK_BATCH < entries.length) continue
        // quota is judged on the whole batch: the last call sees the same
        // free value, so sum what we asked for ourselves
        const rejected: PrecheckItem[] = []
        const accepted: UploadEntry[] = []
        let needed = 0
        for (const e of entries) {
          const v = verdicts.get(entryKey(e.relativePath, e.file.name))
          if (!v || v.status === 'ok' || v.status === 'conflict') {
            accepted.push(e)
            needed += e.file.size
          } else rejected.push(v)
        }
        const quotaOk = res.limit <= 0 ? res.quotaReason !== 'disk' : needed <= res.free
        const quotaReason = quotaOk ? undefined : (res.quotaReason ?? 'quota')
        let chosen = accepted
        if (rejected.length > 0 || !quotaOk) {
          // greedy subset that fits when the quota is short (SPEC §8.8 "lanjut sebagian")
          let fits = accepted
          if (!quotaOk) {
            fits = []
            let left = res.free
            for (const e of accepted) {
              if (e.file.size <= left) {
                fits.push(e)
                left -= e.file.size
              }
            }
          }
          const go = isPublicScope()
            ? true
            : await askUploadPrecheck({
                rejected,
                accepted: accepted.length,
                fits: fits.length,
                needed,
                free: res.free,
                limit: res.limit,
                maxUploadSize: res.maxUploadSize,
                ...(quotaReason ? { quotaReason } : {}),
              })
          if (!go) return 0
          chosen = fits
        }
        const conflicts: Conflict[] = []
        for (const e of chosen) {
          const v = verdicts.get(entryKey(e.relativePath, e.file.name))
          if (v?.status === 'conflict' && v.existing) {
            conflicts.push({
              path: v.path,
              source: entryKey(e.relativePath, e.file.name),
              existing: { size: v.existing.size, mtime: v.existing.mtime, isDir: v.existing.isDir },
              incoming: {
                size: e.file.size,
                mtime: new Date(e.file.lastModified || Date.now()).toISOString(),
                isDir: false,
              },
            })
          }
        }
        let perItem: Record<string, ConflictPolicy> = {}
        if (conflicts.length > 0 && !isPublicScope()) {
          const answer = await askConflicts('copy', conflicts)
          if (!answer) return 0
          perItem = answer
        }
        let started = 0
        for (const e of chosen) {
          const key = entryKey(e.relativePath, e.file.name)
          const policy = perItem[key]
          if (policy === 'skip') {
            this.addSkipped(e, dest)
            continue
          }
          this.enqueue(e, dest, policy === 'overwrite' ? 'overwrite' : 'rename')
          started++
        }
        return started
      }
    } catch (e) {
      const msg =
        e instanceof ApiError
          ? t(`errors:${e.code}`, { defaultValue: e.message })
          : t('upload:toast.precheckFailed')
      announce(msg, true)
      this.emit({
        type: 'error',
        transfer: {
          id: 'precheck',
          name: '',
          relativePath: '',
          dest,
          size: 0,
          sent: 0,
          status: 'error',
          speedBps: null,
          etaSeconds: null,
          error: { code: e instanceof ApiError ? e.code : 'NETWORK', message: msg },
          createdAt: Date.now(),
        },
      })
      return 0
    }
    return 0
  }

  private addSkipped(e: UploadEntry, dest: string): void {
    useTransfersStore.getState().add({
      id: crypto.randomUUID(),
      name: e.file.name,
      relativePath: e.relativePath,
      dest,
      size: e.file.size,
      sent: 0,
      status: 'skipped',
      speedBps: null,
      etaSeconds: null,
      result: 'skipped',
      createdAt: Date.now(),
    })
    this.batch.skipped++
  }

  // --- queue --------------------------------------------------------------

  enqueue(e: UploadEntry, dest: string, policy: UploadPolicy): string {
    const id = crypto.randomUUID()
    this.files.set(id, { file: e.file, policy })
    useTransfersStore.getState().add({
      id,
      name: e.file.name,
      relativePath: e.relativePath,
      dest,
      size: e.file.size,
      sent: 0,
      status: 'queued',
      speedBps: null,
      etaSeconds: null,
      createdAt: Date.now(),
    })
    this.waiting.push(id)
    this.pump()
    return id
  }

  private pump(): void {
    while (this.live.size < CONCURRENCY && this.waiting.length > 0) {
      const id = this.waiting.shift()!
      void this.start(id)
    }
    if (this.live.size === 0 && this.waiting.length === 0) this.drained()
  }

  private drained(): void {
    const b = this.batch
    if (b.done + b.skipped + b.errors === 0) return
    this.batch = { done: 0, skipped: 0, errors: 0, dests: new Set() }
    this.emit({
      type: 'drained',
      done: b.done,
      skipped: b.skipped,
      errors: b.errors,
      dests: [...b.dests],
    })
  }

  private transfer(id: string): Transfer | undefined {
    return useTransfersStore.getState().items[id]
  }

  private setStatus(id: string, status: TransferStatus, patch: Partial<Transfer> = {}): void {
    useTransfersStore.getState().update(id, { status, ...patch })
  }

  private async start(id: string): Promise<void> {
    const rec = this.files.get(id)
    const tr = this.transfer(id)
    if (!rec || !tr) return
    // the slot is taken synchronously so pump() cannot start more than
    // CONCURRENCY uploads while the tus module is still loading
    const entry: LiveUpload = { upload: null, silenced: false }
    this.live.set(id, entry)
    this.setStatus(id, 'uploading', { detached: false })
    let tus: TusModule
    try {
      tus = await loadTus()
    } catch (e) {
      this.live.delete(id)
      this.setStatus(id, 'error', {
        error: { code: 'NETWORK', message: e instanceof Error ? e.message : String(e) },
      })
      this.pump()
      return
    }
    if (this.live.get(id) !== entry) return // paused/cancelled while loading
    const metadata: Record<string, string> = {
      filename: rec.file.name,
      path: tr.dest,
      onConflict: rec.policy,
    }
    if (tr.relativePath) metadata.relativePath = tr.relativePath
    if (rec.file.lastModified > 0) metadata.lastModified = String(rec.file.lastModified)
    // the server's UPLOAD_CHUNK_SIZE wins over the default even when no
    // precheck ran in this page load (resume after a reload)
    const cfgChunk = useSessionStore.getState().config?.chunkSize
    if (cfgChunk && cfgChunk > 0) this.chunkSize = cfgChunk
    const options: UploadOptions = {
      endpoint: isPublicScope() ? `${publicBase()}/upload/tus/` : TUS_ENDPOINT,
      chunkSize: this.chunkSize,
      retryDelays: RETRY_DELAYS,
      parallelUploads: 1,
      storeFingerprintForResuming: true,
      removeFingerprintOnSuccess: true,
      metadata,
      // the CSRF token is read per request (not via `headers`, which would
      // be appended a second time by XHR when both are set)
      onBeforeRequest: (req) => {
        const token = useSessionStore.getState().csrfToken
        if (token) req.setHeader('X-CSRF-Token', token)
      },
      onShouldRetry: (err: DetailedError) => {
        const status = err.originalResponse?.getStatus() ?? 0
        // 4xx are final except "locked" and "rate limited" (SPEC §6.5)
        if (status >= 400 && status < 500 && status !== 423 && status !== 429) return false
        return true
      },
      onUploadUrlAvailable: () => {
        useTransfersStore.getState().update(id, { uploadUrl: entry.upload?.url ?? null })
      },
      onProgress: (sent, total) => this.progress(id, sent, total),
      onSuccess: (payload) => {
        this.live.delete(id)
        this.files.delete(id)
        this.samples.delete(id)
        const result = (payload.lastResponse.getHeader('Upload-Result') ||
          'created') as TransferResult
        const rawPath = payload.lastResponse.getHeader('Upload-Path') || ''
        let finalPath: string | null = null
        if (rawPath) {
          try {
            finalPath = decodeURIComponent(rawPath)
          } catch {
            finalPath = rawPath
          }
        }
        const status: TransferStatus = result === 'skipped' ? 'skipped' : 'done'
        this.setStatus(id, status, {
          sent: rec.file.size,
          speedBps: null,
          etaSeconds: null,
          result,
          finalPath,
          uploadUrl: entry.upload?.url ?? null,
        })
        if (status === 'done') this.batch.done++
        else this.batch.skipped++
        this.batch.dests.add(tr.dest)
        const done = this.transfer(id)
        if (done) this.emit({ type: 'done', transfer: done })
        this.pump()
      },
      onError: (err) => {
        this.live.delete(id)
        this.samples.delete(id)
        if (entry.silenced) return
        const parsed = parseTusError(err)
        this.setStatus(id, 'error', { error: parsed, speedBps: null, etaSeconds: null })
        this.batch.errors++
        const failed = this.transfer(id)
        if (failed) this.emit({ type: 'error', transfer: failed })
        if (parsed.code === 'QUOTA_EXCEEDED') this.failWaiting(parsed)
        this.pump()
      },
    }
    const upload = new tus.Upload(rec.file, options)
    entry.upload = upload
    try {
      const previous = await upload.findPreviousUploads()
      const match = previous.find((p) => p.uploadUrl) ?? previous[0]
      if (match) {
        upload.resumeFromPreviousUpload(match)
        useTransfersStore.getState().update(id, { uploadUrl: match.uploadUrl })
      }
    } catch {
      /* no url storage: start fresh */
    }
    if (this.live.get(id) !== entry) return // paused/cancelled while looking up
    upload.start()
  }

  /** QUOTA_EXCEEDED stops the rest of the queue (SPEC §8.8 step 6). */
  private failWaiting(error: { code: string; message: string }): void {
    while (this.waiting.length > 0) {
      const id = this.waiting.shift()!
      this.files.delete(id)
      this.setStatus(id, 'error', { error })
      this.batch.errors++
    }
  }

  private progress(id: string, sent: number, total: number): void {
    const now = Date.now()
    const list = this.samples.get(id) ?? []
    list.push({ t: now, sent })
    while (list.length > 1 && now - list[0]!.t > SPEED_WINDOW_MS) list.shift()
    this.samples.set(id, list)
    const tr = this.transfer(id)
    if (tr && tr.resumedFrom === undefined && list.length === 1 && sent > 0 && sent < total) {
      // first progress event after a resume already carries the server offset
      useTransfersStore.getState().update(id, { resumedFrom: sent })
    }
    const last = this.lastStoreUpdate.get(id) ?? 0
    if (now - last < STORE_THROTTLE_MS && sent < total) return
    this.lastStoreUpdate.set(id, now)
    let speedBps: number | null = null
    let etaSeconds: number | null = null
    const first = list[0]!
    if (list.length > 1 && now > first.t) {
      speedBps = ((sent - first.sent) * 1000) / (now - first.t)
      if (speedBps > 0) etaSeconds = Math.max(0, Math.round((total - sent) / speedBps))
    }
    useTransfersStore.getState().update(id, { sent, speedBps, etaSeconds })
  }

  // --- controls -----------------------------------------------------------

  pause(id: string): void {
    const l = this.live.get(id)
    if (l) {
      l.silenced = true
      void l.upload?.abort(false).catch(() => undefined)
      this.live.delete(id)
      this.samples.delete(id)
    } else {
      const i = this.waiting.indexOf(id)
      if (i >= 0) this.waiting.splice(i, 1)
      else return
    }
    this.setStatus(id, 'paused', { speedBps: null, etaSeconds: null })
    this.pump()
  }

  resume(id: string): void {
    const tr = this.transfer(id)
    if (!tr || (tr.status !== 'paused' && tr.status !== 'error')) return
    if (!this.files.has(id)) {
      useTransfersStore.getState().update(id, { detached: true })
      return
    }
    this.setStatus(id, 'queued', { error: undefined, detached: false })
    this.waiting.push(id)
    this.pump()
  }

  retry(id: string): void {
    this.resume(id)
  }

  cancel(id: string): void {
    const l = this.live.get(id)
    if (l) {
      l.silenced = true
      void l.upload?.abort(true).catch(() => undefined)
      this.live.delete(id)
      this.samples.delete(id)
    } else {
      const i = this.waiting.indexOf(id)
      if (i >= 0) this.waiting.splice(i, 1)
    }
    this.files.delete(id)
    const tr = this.transfer(id)
    if (
      tr &&
      (tr.status === 'queued' ||
        tr.status === 'uploading' ||
        tr.status === 'paused' ||
        tr.status === 'error')
    ) {
      this.setStatus(id, 'cancelled', { speedBps: null, etaSeconds: null })
    }
    this.pump()
  }

  pauseAll(): void {
    for (const id of [...this.waiting, ...this.live.keys()]) this.pause(id)
  }

  resumeAll(): void {
    const s = useTransfersStore.getState()
    for (const id of s.order) {
      const tr = s.items[id]
      if (tr && (tr.status === 'paused' || tr.status === 'error') && this.files.has(id))
        this.resume(id)
    }
  }

  cancelAll(): void {
    const s = useTransfersStore.getState()
    for (const id of s.order) {
      const tr = s.items[id]
      if (tr && (tr.status === 'queued' || tr.status === 'uploading' || tr.status === 'paused'))
        this.cancel(id)
    }
  }

  /** Detached transfers (after a reload) matched by name and size against
   *  the files the user picked again; tus resumes from its fingerprint. */
  attach(files: Iterable<File>, policy: UploadPolicy = 'rename'): number {
    const s = useTransfersStore.getState()
    const pending = s.order
      .map((id) => s.items[id])
      .filter(
        (tr): tr is Transfer =>
          !!tr && !!tr.detached && (tr.status === 'paused' || tr.status === 'error'),
      )
    let matched = 0
    for (const file of files) {
      const rel = typeof file.webkitRelativePath === 'string' ? file.webkitRelativePath : ''
      const dir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : ''
      const idx = pending.findIndex(
        (tr) =>
          tr.name === file.name && tr.size === file.size && (dir === '' || tr.relativePath === dir),
      )
      if (idx < 0) continue
      const tr = pending.splice(idx, 1)[0]!
      this.files.set(tr.id, { file, policy })
      useTransfersStore.getState().update(tr.id, { detached: false })
      this.resume(tr.id)
      matched++
    }
    return matched
  }

  /** Test helper. */
  reset(): void {
    for (const id of [...this.live.keys()]) {
      const l = this.live.get(id)!
      l.silenced = true
      void l.upload?.abort(false).catch(() => undefined)
    }
    this.live.clear()
    this.files.clear()
    this.waiting.length = 0
    this.samples.clear()
    this.lastStoreUpdate.clear()
    this.batch = { done: 0, skipped: 0, errors: 0, dests: new Set() }
  }
}

/** Error envelope out of a tus failure (our JSON body or a protocol error). */
export function parseTusError(err: Error | DetailedError): { code: string; message: string } {
  const detailed = err as Partial<DetailedError>
  const res = detailed.originalResponse ?? null
  if (res) {
    const status = res.getStatus()
    const body = res.getBody?.() ?? ''
    try {
      const parsed = JSON.parse(body) as { error?: { code?: string; message?: string } }
      if (parsed.error?.code) {
        return { code: parsed.error.code, message: parsed.error.message ?? '' }
      }
    } catch {
      /* not our envelope */
    }
    if (status === 413) return { code: 'PAYLOAD_TOO_LARGE', message: body }
    if (status === 401) return { code: 'UNAUTHENTICATED', message: body }
    if (status === 403) return { code: 'FORBIDDEN', message: body }
    if (status === 404) return { code: 'NOT_FOUND', message: body }
    if (status >= 500) return { code: 'INTERNAL', message: body }
    return { code: `HTTP_${status}`, message: body }
  }
  return { code: 'NETWORK', message: err.message }
}

export const uploader = new Uploader()

/** Human-readable error line for a transfer. */
export function transferErrorMessage(tr: Transfer): string {
  const code = tr.error?.code ?? 'UNKNOWN'
  const specific = t(`upload:error.${code}`, { defaultValue: '' })
  if (specific) return specific
  return t(`errors:${code}`, { defaultValue: tr.error?.message || t('errors:UNKNOWN') })
}

// --- entry points ---------------------------------------------------------

export async function uploadEntries(entries: UploadEntry[], dest: string): Promise<number> {
  return uploader.plan(entries, dest)
}

export async function uploadFromFileList(files: FileList | File[], dest: string): Promise<number> {
  const { entries, truncated } = entriesFromFileList(files)
  if (truncated) announce(t('upload:toast.truncated', { max: entries.length }), true)
  return uploader.plan(entries, dest)
}

export async function uploadFromDataTransfer(dt: DataTransfer, dest: string): Promise<number> {
  const { entries, truncated } = await entriesFromDataTransfer(dt)
  if (truncated) announce(t('upload:toast.truncated', { max: entries.length }), true)
  return uploader.plan(entries, dest)
}

export async function uploadFromClipboard(dt: DataTransfer, dest: string): Promise<number> {
  return uploader.plan(entriesFromClipboard(dt), dest)
}

export interface UploaderApi {
  files: (files: FileList | File[], dest: string) => Promise<number>
  drop: (dt: DataTransfer, dest: string) => Promise<number>
  clipboard: (dt: DataTransfer, dest: string) => Promise<number>
  pause: (id: string) => void
  resume: (id: string) => void
  cancel: (id: string) => void
  retry: (id: string) => void
  pauseAll: () => void
  resumeAll: () => void
  cancelAll: () => void
  attach: (files: Iterable<File>) => number
}

/** Stable handle on the module queue for components. */
export function useUploader(): UploaderApi {
  return useMemo(
    () => ({
      files: uploadFromFileList,
      drop: uploadFromDataTransfer,
      clipboard: uploadFromClipboard,
      pause: (id) => uploader.pause(id),
      resume: (id) => uploader.resume(id),
      cancel: (id) => uploader.cancel(id),
      retry: (id) => uploader.retry(id),
      pauseAll: () => uploader.pauseAll(),
      resumeAll: () => uploader.resumeAll(),
      cancelAll: () => uploader.cancelAll(),
      attach: (files) => uploader.attach(files),
    }),
    [],
  )
}
