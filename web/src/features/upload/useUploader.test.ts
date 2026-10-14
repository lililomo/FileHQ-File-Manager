import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { UploadOptions } from 'tus-js-client'
import { useFileOpsDialogs } from '@/features/file-ops/store'
import { resetTransfersStore, useTransfersStore } from '@/stores/transfers'
import { useSessionStore } from '@/stores/session'
import type { PrecheckResult } from './api'
import {
  CONCURRENCY,
  parseTusError,
  setTusModule,
  uploadEntries,
  uploader,
  type UploaderEvent,
} from './useUploader'

const precheck =
  vi.fn<(path: string, files: { name: string; size: number }[]) => Promise<PrecheckResult>>()
vi.mock('./api', async (orig) => {
  const mod = await orig<typeof import('./api')>()
  return {
    ...mod,
    precheckUpload: (p: string, f: { name: string; size: number }[]) => precheck(p, f),
  }
})

/** Scripted tus.Upload: the test finishes/fails each upload by hand. */
class FakeUpload {
  static instances: FakeUpload[] = []
  url: string | null = null
  started = 0
  aborted: boolean[] = []
  previous: { uploadUrl: string }[] = []
  constructor(
    public file: File,
    public options: UploadOptions,
  ) {
    FakeUpload.instances.push(this)
  }
  start() {
    this.started++
    this.url = this.url ?? `http://x/api/v1/upload/tus/${FakeUpload.instances.indexOf(this)}`
    this.options.onUploadUrlAvailable?.()
  }
  abort(terminate?: boolean) {
    this.aborted.push(!!terminate)
    return Promise.resolve()
  }
  findPreviousUploads() {
    return Promise.resolve(this.previous as never)
  }
  resumeFromPreviousUpload(p: { uploadUrl: string }) {
    this.url = p.uploadUrl
  }
  succeed(headers: Record<string, string> = {}) {
    this.options.onProgress?.(this.file.size, this.file.size)
    this.options.onSuccess?.({
      lastResponse: {
        getStatus: () => 204,
        getHeader: (h: string) => headers[h],
        getBody: () => '',
        getUnderlyingObject: () => null,
      },
    })
  }
  fail(status: number, body: string) {
    const err = new Error('tus') as Error & { originalResponse: unknown }
    err.originalResponse = {
      getStatus: () => status,
      getHeader: () => undefined,
      getBody: () => body,
      getUnderlyingObject: () => null,
    }
    this.options.onError?.(err as never)
  }
}

const okResult = (files: { name: string; size: number }[]): PrecheckResult => ({
  items: files.map((f) => ({ name: f.name, path: `/dest/${f.name}`, status: 'ok' })),
  quotaOk: true,
  needed: files.reduce((a, f) => a + f.size, 0),
  free: 1 << 30,
  limit: 1 << 30,
  maxUploadSize: 1 << 30,
  chunkSize: 1 << 20,
})

const file = (name: string, size = 10) => ({
  file: new File([new Uint8Array(size)], name),
  relativePath: '',
})
const tick = () => new Promise((r) => setTimeout(r, 0))
const items = () => useTransfersStore.getState().items
const statuses = () =>
  useTransfersStore.getState().order.map((id) => useTransfersStore.getState().items[id]!.status)

describe('uploader queue', () => {
  const events: UploaderEvent[] = []
  let off: () => void
  beforeEach(() => {
    resetTransfersStore()
    uploader.reset()
    FakeUpload.instances = []
    events.length = 0
    off = uploader.subscribe((e) => events.push(e))
    setTusModule({ Upload: FakeUpload as never })
    useSessionStore.getState().setCsrfToken('csrf-1')
    precheck.mockImplementation((_p, files) => Promise.resolve(okResult(files)))
  })
  afterEach(() => {
    off()
    setTusModule(null)
    useFileOpsDialogs.getState().close()
  })

  it('runs at most CONCURRENCY uploads and pulls the next one on success', async () => {
    const n = await uploadEntries(
      ['a', 'b', 'c', 'd', 'e'].map((x) => file(`${x}.txt`)),
      '/dest',
    )
    expect(n).toBe(5)
    await tick()
    expect(FakeUpload.instances).toHaveLength(CONCURRENCY)
    expect(statuses()).toEqual(['uploading', 'uploading', 'uploading', 'queued', 'queued'])
    const first = FakeUpload.instances[0]!
    expect(first.options.metadata).toMatchObject({
      filename: 'a.txt',
      path: '/dest',
      onConflict: 'rename',
    })
    const headers: Record<string, string> = {}
    await first.options.onBeforeRequest?.({
      setHeader: (k: string, v: string) => (headers[k] = v),
    } as never)
    expect(headers).toEqual({ 'X-CSRF-Token': 'csrf-1' })
    expect(first.options.headers).toBeUndefined()
    expect(first.options.chunkSize).toBe(1 << 20)
    first.succeed({ 'Upload-Result': 'created', 'Upload-Path': '/dest/a.txt' })
    await tick()
    expect(FakeUpload.instances).toHaveLength(4)
    expect(statuses()).toEqual(['done', 'uploading', 'uploading', 'uploading', 'queued'])
    const a = Object.values(items()).find((t) => t.name === 'a.txt')!
    expect(a.finalPath).toBe('/dest/a.txt')
    expect(events.filter((e) => e.type === 'done')).toHaveLength(1)
    for (const u of FakeUpload.instances.slice(1))
      u.succeed({ 'Upload-Result': 'renamed', 'Upload-Path': '/dest/x%20(1).txt' })
    await tick()
    for (const u of FakeUpload.instances.slice(4)) u.succeed()
    await tick()
    expect(statuses()).toEqual(['done', 'done', 'done', 'done', 'done'])
    const drained = events.find((e) => e.type === 'drained')
    expect(drained).toMatchObject({
      type: 'drained',
      done: 5,
      skipped: 0,
      errors: 0,
      dests: ['/dest'],
    })
  })

  it('pauses (abort without terminate), resumes from the stored url and cancels (terminate)', async () => {
    await uploadEntries([file('p.txt'), file('q.txt')], '/dest')
    await tick()
    const [p, q] = FakeUpload.instances as [FakeUpload, FakeUpload]
    const pid = useTransfersStore.getState().order[0]!
    uploader.pause(pid)
    expect(p.aborted).toEqual([false])
    expect(items()[pid]!.status).toBe('paused')
    uploader.resume(pid)
    await tick()
    const resumed = FakeUpload.instances[2]!
    resumed.previous = [{ uploadUrl: p.url! }]
    expect(items()[pid]!.status).toBe('uploading')
    const qid = useTransfersStore.getState().order[1]!
    uploader.cancel(qid)
    expect(q.aborted).toEqual([true])
    expect(items()[qid]!.status).toBe('cancelled')
  })

  it('asks for conflicts, skips without uploading and respects per-file answers', async () => {
    precheck.mockImplementation((_p, files) =>
      Promise.resolve({
        ...okResult(files),
        items: files.map((f) => ({
          name: f.name,
          path: `/dest/${f.name}`,
          status: 'conflict' as const,
          existing: { size: 5, mtime: '2026-01-01T00:00:00Z', isDir: false },
        })),
      }),
    )
    const planned = uploadEntries([file('x.txt'), file('y.txt'), file('z.txt')], '/dest')
    await tick()
    const dialog = useFileOpsDialogs.getState().dialog
    expect(dialog?.kind).toBe('conflict')
    if (dialog?.kind !== 'conflict') throw new Error('no conflict dialog')
    expect(dialog.conflicts.map((c) => c.source)).toEqual(['x.txt', 'y.txt', 'z.txt'])
    dialog.resolve({ 'x.txt': 'overwrite', 'y.txt': 'skip', 'z.txt': 'rename' })
    expect(await planned).toBe(2)
    await tick()
    expect(FakeUpload.instances.map((u) => u.options.metadata?.onConflict)).toEqual([
      'overwrite',
      'rename',
    ])
    const y = Object.values(items()).find((t) => t.name === 'y.txt')!
    expect(y.status).toBe('skipped')
  })

  it('shows rejected files and the quota shortfall before anything starts', async () => {
    precheck.mockImplementation((_p, files) =>
      Promise.resolve({
        ...okResult(files),
        items: files.map((f) => ({
          name: f.name,
          path: `/dest/${f.name}`,
          status: f.name.endsWith('.exe') ? ('blocked_ext' as const) : ('ok' as const),
          ...(f.name.endsWith('.exe') ? { reason: 'exe' } : {}),
        })),
        quotaOk: false,
        quotaReason: 'quota' as const,
        free: 15,
      }),
    )
    const planned = uploadEntries([file('a.exe'), file('b.txt', 10), file('c.txt', 10)], '/dest')
    await tick()
    const dialog = useFileOpsDialogs.getState().dialog
    expect(dialog?.kind).toBe('uploadPrecheck')
    if (dialog?.kind !== 'uploadPrecheck') throw new Error('no precheck dialog')
    expect(dialog.info).toMatchObject({
      accepted: 2,
      fits: 1,
      needed: 20,
      free: 15,
      quotaReason: 'quota',
    })
    expect(dialog.info.rejected.map((r) => r.name)).toEqual(['a.exe'])
    dialog.resolve(true)
    expect(await planned).toBe(1)
    await tick()
    expect(FakeUpload.instances.map((u) => u.file.name)).toEqual(['b.txt'])
    // cancelling the dialog starts nothing
    FakeUpload.instances = []
    const planned2 = uploadEntries([file('a.exe'), file('b.txt', 10)], '/dest')
    await tick()
    const d2 = useFileOpsDialogs.getState().dialog
    if (d2?.kind !== 'uploadPrecheck') throw new Error('no precheck dialog')
    d2.resolve(false)
    expect(await planned2).toBe(0)
    expect(FakeUpload.instances).toHaveLength(0)
  })

  it('maps the error envelope and stops the queue on QUOTA_EXCEEDED', async () => {
    await uploadEntries(
      ['a', 'b', 'c', 'd'].map((x) => file(`${x}.txt`)),
      '/dest',
    )
    await tick()
    FakeUpload.instances[0]!.fail(
      413,
      JSON.stringify({
        error: { code: 'QUOTA_EXCEEDED', message: 'storage quota exceeded', details: {} },
      }),
    )
    await tick()
    expect(statuses()).toEqual(['error', 'uploading', 'uploading', 'error'])
    const a = Object.values(items()).find((t) => t.name === 'a.txt')!
    expect(a.error).toEqual({ code: 'QUOTA_EXCEEDED', message: 'storage quota exceeded' })
    expect(parseTusError(new Error('offline'))).toEqual({ code: 'NETWORK', message: 'offline' })
    expect(events.filter((e) => e.type === 'error')).toHaveLength(1)
  })

  it('does not retry on 4xx except 423/429 and retries on 5xx/network', async () => {
    await uploadEntries([file('r.txt')], '/dest')
    await tick()
    const should = FakeUpload.instances[0]!.options.onShouldRetry!
    const err = (status: number) => ({ originalResponse: { getStatus: () => status } }) as never
    expect(should(err(400), 0, {} as never)).toBe(false)
    expect(should(err(404), 0, {} as never)).toBe(false)
    expect(should(err(423), 0, {} as never)).toBe(true)
    expect(should(err(429), 0, {} as never)).toBe(true)
    expect(should(err(500), 0, {} as never)).toBe(true)
    expect(should({ originalResponse: null } as never, 0, {} as never)).toBe(true)
  })

  it('re-attaches picked files to detached transfers by name and size', async () => {
    useTransfersStore.getState().add({
      id: 'left',
      name: 'left.bin',
      relativePath: '',
      dest: '/dest',
      size: 10,
      sent: 4,
      status: 'paused',
      detached: true,
      speedBps: null,
      etaSeconds: null,
      createdAt: 1,
    })
    expect(
      uploader.attach([
        new File([new Uint8Array(3)], 'other.bin'),
        new File([new Uint8Array(10)], 'left.bin'),
      ]),
    ).toBe(1)
    await tick()
    expect(items().left!.detached).toBe(false)
    expect(items().left!.status).toBe('uploading')
    expect(FakeUpload.instances[0]!.file.name).toBe('left.bin')
  })
})
