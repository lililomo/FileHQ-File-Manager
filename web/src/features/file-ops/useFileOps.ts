import { useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Item, ListResponse } from '@/features/browser/api'
import { joinPath, splitPath } from '@/features/browser/path'
import { announce } from '@/lib/announce'
import { invalidateMediaUrl } from '@/lib/media-url'
import { ApiError } from '@/lib/api'
import { formatBytes, formatNumber } from '@/lib/format'
import { useClipboardStore } from '@/stores/clipboard'
import { getPref } from '@/stores/prefs'
import { QUOTA_QUERY_KEY } from '@/features/quota/api'
import { taskLabel } from '@/features/tasks/taskLabel'
import { uploadFromDataTransfer } from '@/features/upload/useUploader'
import { useTasksStore } from '@/stores/tasks'
import {
  batchRenameApply,
  chmodItems,
  conflictsOf,
  createSymlink,
  deleteItems,
  deletePermanentItems,
  dirSize,
  duplicateItem,
  isTaskRequired,
  renameItem,
  transferItems,
  undoOperation,
  type ChmodInput,
  type ConflictPolicy,
  type DirSizeResult,
  type OpResponse,
  type RenameRule,
  type TransferOp,
  type TransferPolicy,
} from './api'
import { parentOf } from './dnd'
import { askConflicts, askDelete, canUndoNow, useUndoStore } from './store'
import { showUndoToast } from './undoToast'

type ListData = InfiniteData<ListResponse, string | null>
type LooseT = (key: string, options?: Record<string, unknown>) => string

/** Optimistic patch of every cached listing of `dir` (all sort variants). */
export function patchLists(qc: QueryClient, dir: string, fn: (items: Item[]) => Item[]): void {
  qc.setQueriesData<ListData>({ queryKey: ['list', dir] }, (old) => {
    // the folder picker caches plain ListResponse pages under the same prefix
    if (!old || !Array.isArray(old.pages)) return old
    let delta = 0
    const pages = old.pages.map((p) => {
      const items = fn(p.items)
      delta += items.length - p.items.length
      return { ...p, items }
    })
    if (pages[0]) pages[0] = { ...pages[0], total: Math.max(0, pages[0].total + delta) }
    return { ...old, pages }
  })
}

type Snapshot = [readonly unknown[], unknown][]
function snapshot(qc: QueryClient, dirs: Iterable<string>): Snapshot {
  const out: Snapshot = []
  for (const d of new Set(dirs)) out.push(...qc.getQueriesData({ queryKey: ['list', d] }))
  return out
}
function restore(qc: QueryClient, snap: Snapshot): void {
  for (const [key, data] of snap) qc.setQueryData(key, data)
}

/** Relative symlink target from the link's folder to `target` (SPEC §5.4). */
export function relativeTarget(linkDir: string, target: string): string {
  const a = splitPath(linkDir)
  const b = splitPath(target)
  let i = 0
  while (i < a.length && i < b.length && a[i] === b[i]) i++
  const up = a.slice(i).map(() => '..')
  const rest = b.slice(i)
  const parts = [...up, ...rest]
  return parts.length === 0 ? '.' : parts.join('/')
}

export function errorMessage(t: LooseT, e: unknown): string {
  if (isTaskRequired(e)) return t('fileops:error.taskRequired')
  if (e instanceof ApiError) {
    const reason = typeof e.details.reason === 'string' ? e.details.reason : ''
    if (reason) {
      const specific = t(`fileops:error.${reason}`, { defaultValue: '' })
      if (specific) return specific
      const generic = t(`errors:reason.${reason}`, { defaultValue: '' })
      if (generic) return `${t(`errors:${e.code}`, { defaultValue: e.message })} (${generic})`
    }
    const field = Object.values(e.fields)[0]
    if (e.code === 'NAME_INVALID' && field) {
      const r = t(`errors:reason.${field}`, { defaultValue: '' })
      if (r) return `${t('errors:NAME_INVALID')} ${r}`
    }
    return t(`errors:${e.code}`, { defaultValue: e.message })
  }
  return e instanceof Error ? e.message : String(e)
}

export interface FileOps {
  copy: (paths: string[], from: string, tabId: string | null) => void
  cut: (paths: string[], from: string, tabId: string | null) => void
  /** Paste the clipboard into `toDir`. */
  paste: (toDir: string) => Promise<void>
  transfer: (
    op: TransferOp,
    paths: string[],
    to: string,
    policy?: TransferPolicy,
  ) => Promise<OpResponse | null>
  rename: (path: string, newName: string) => Promise<OpResponse | null>
  duplicate: (path: string) => Promise<OpResponse | null>
  /** Delete to the Recycle Bin (asks first when `fileops.confirm_delete`). */
  remove: (paths: string[]) => Promise<OpResponse | null>
  /** Shift+Delete flow (dialog + `HAPUS` for > 50 items); bypasses the Recycle Bin. */
  removePermanent: (paths: string[]) => Promise<OpResponse | null>
  /** "Hitung ukuran": walks the folder and patches the Ukuran column (Step 10a). */
  dirSize: (path: string) => Promise<DirSizeResult | null>
  chmod: (input: ChmodInput) => Promise<OpResponse | null>
  /** `target` is an absolute virtual path; converted to a relative link. */
  symlink: (linkPath: string, target: string) => Promise<OpResponse | null>
  applyBatchRename: (paths: string[], rule: RenameRule) => Promise<OpResponse | null>
  undo: () => Promise<void>
  /** Files dropped from the OS go to the upload queue (Step 12a). */
  osDrop: (files: DataTransfer, toDir: string) => void
}

export function useFileOps(): FileOps {
  const qc = useQueryClient()
  const { t, i18n } = useTranslation(['fileops', 'errors', 'tasks'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const lt = t as unknown as LooseT

  const invalidate = useCallback(
    (dirs: Iterable<string>) => {
      for (const d of new Set(dirs)) void qc.invalidateQueries({ queryKey: ['list', d] })
      void qc.invalidateQueries({ queryKey: QUOTA_QUERY_KEY })
    },
    [qc],
  )
  const fail = useCallback(
    (e: unknown) => {
      const msg = errorMessage(lt, e)
      toast.error(msg)
      announce(msg, true)
    },
    [lt],
  )
  const undo = useCallback(async () => {
    const entry = useUndoStore.getState().entry
    if (!canUndoNow(entry)) return
    useUndoStore.getState().set(null)
    toast.dismiss(entry!.toastId)
    try {
      await undoOperation(entry!.token)
      await qc.invalidateQueries({ queryKey: ['list'] })
      const msg = t('fileops:toast.undone', { what: entry!.label })
      toast.success(msg)
      announce(msg)
    } catch (e) {
      fail(e)
      void qc.invalidateQueries({ queryKey: ['list'] })
    }
  }, [qc, t, fail])
  const done = useCallback(
    (message: string, res: OpResponse) => {
      announce(message)
      if (res.undoToken) showUndoToast(message, res.undoToken, () => void undo())
      else toast.success(message)
      if (res.warnings?.includes('windows_incompatible')) toast.warning(t('fileops:warnWindows'))
    },
    [undo, t],
  )
  const n = useCallback((count: number) => formatNumber(count, lang), [lang])
  const queuedTask = useCallback(
    (task: NonNullable<OpResponse['task']>) => {
      useTasksStore.getState().upsert(task)
      useTasksStore.getState().setPanel(true)
      const msg = t('tasks:toast.queued', { title: taskLabel(task, lt, lang) })
      toast.info(msg)
      announce(msg)
    },
    [t, lt, lang],
  )

  const transfer = useCallback<FileOps['transfer']>(
    async (op, paths, to, policy = {}) => {
      if (paths.length === 0) return null
      const sources = paths.map(parentOf)
      const snap = snapshot(qc, [...sources, to])
      if (op === 'move') {
        const set = new Set(paths)
        for (const d of new Set(sources))
          patchLists(qc, d, (items) => items.filter((it) => !set.has(it.path)))
        // signed viewer URLs of the old paths would only 404 (Step 12b note)
        for (const p of paths) invalidateMediaUrl(p)
      }
      // retry with per-item answers until the server stops reporting conflicts
      const run = async (): Promise<OpResponse | null> => {
        let perItem: Record<string, ConflictPolicy> = { ...(policy.perItem ?? {}) }
        for (;;) {
          try {
            return await transferItems(op, paths, to, { ...policy, perItem })
          } catch (e) {
            const conflicts = conflictsOf(e)
            if (!conflicts || conflicts.length === 0) throw e
            const answer = await askConflicts(op, conflicts)
            if (!answer) return null
            perItem = { ...perItem, ...answer }
          }
        }
      }
      let res: OpResponse | null
      try {
        res = await run()
      } catch (e) {
        restore(qc, snap)
        fail(e)
        return null
      }
      if (!res) {
        restore(qc, snap)
        return null
      }
      if (res.task) {
        // beyond the synchronous limits: the server queued a task (Step 11)
        restore(qc, snap)
        queuedTask(res.task)
        return res
      }
      invalidate([...sources, to])
      const okCount = res.results.filter((r) => r.status !== 'skipped').length
      const skipped = res.results.length - okCount
      let msg = t(op === 'move' ? 'fileops:toast.moved' : 'fileops:toast.copied', {
        count: okCount,
        n: n(okCount),
      })
      if (skipped > 0) msg += ` ${t('fileops:toast.skipped', { count: skipped, n: n(skipped) })}`
      done(msg, res)
      return res
    },
    [qc, invalidate, fail, done, t, n, queuedTask],
  )

  const copy = useCallback<FileOps['copy']>(
    (paths, from, tabId) => {
      if (paths.length === 0) return
      useClipboardStore.getState().set({ mode: 'copy', paths, from, sourceTab: tabId })
      announce(t('fileops:announce.copied', { count: paths.length, n: n(paths.length) }))
    },
    [t, n],
  )
  const cut = useCallback<FileOps['cut']>(
    (paths, from, tabId) => {
      if (paths.length === 0) return
      useClipboardStore.getState().set({ mode: 'cut', paths, from, sourceTab: tabId })
      announce(t('fileops:announce.cut', { count: paths.length, n: n(paths.length) }))
    },
    [t, n],
  )
  const paste = useCallback<FileOps['paste']>(
    async (toDir) => {
      const data = useClipboardStore.getState().data
      if (!data || data.paths.length === 0) return
      const res = await transfer(data.mode === 'cut' ? 'move' : 'copy', data.paths, toDir)
      if (res && data.mode === 'cut') useClipboardStore.getState().clear()
    },
    [transfer],
  )

  const rename = useCallback<FileOps['rename']>(
    async (path, newName) => {
      const dir = parentOf(path)
      const newPath = joinPath(dir, newName)
      const snap = snapshot(qc, [dir])
      patchLists(qc, dir, (items) =>
        items.map((it) => (it.path === path ? { ...it, name: newName, path: newPath } : it)),
      )
      try {
        const res = await renameItem(path, newName)
        invalidateMediaUrl(path)
        invalidate([dir])
        done(t('fileops:toast.renamed', { name: newName }), res)
        return res
      } catch (e) {
        restore(qc, snap)
        fail(e)
        return null
      }
    },
    [qc, invalidate, done, fail, t],
  )

  const duplicate = useCallback<FileOps['duplicate']>(
    async (path) => {
      try {
        const res = await duplicateItem(path)
        invalidate([parentOf(path)])
        done(
          t('fileops:toast.duplicated', { name: res.item?.name ?? res.results[0]?.to ?? '' }),
          res,
        )
        return res
      } catch (e) {
        fail(e)
        return null
      }
    },
    [invalidate, done, fail, t],
  )

  const deleteNow = useCallback(
    async (paths: string[], labelKey: string) => {
      const dirs = paths.map(parentOf)
      const snap = snapshot(qc, dirs)
      const set = new Set(paths)
      for (const d of new Set(dirs))
        patchLists(qc, d, (items) => items.filter((it) => !set.has(it.path)))
      try {
        const res = await deleteItems(paths)
        for (const p of paths) invalidateMediaUrl(p)
        invalidate(dirs)
        done(lt(labelKey, { count: paths.length, n: n(paths.length) }), res)
        return res
      } catch (e) {
        restore(qc, snap)
        fail(e)
        return null
      }
    },
    [qc, invalidate, done, fail, lt, n],
  )
  const remove = useCallback<FileOps['remove']>(
    async (paths) => {
      if (paths.length === 0) return null
      if (getPref<boolean>('fileops.confirm_delete', true)) {
        const ok = await askDelete('deleteConfirm', paths)
        if (!ok) return null
      }
      return deleteNow(paths, 'fileops:toast.deleted')
    },
    [deleteNow],
  )
  const removePermanent = useCallback<FileOps['removePermanent']>(
    async (paths) => {
      if (paths.length === 0) return null
      const ok = await askDelete('deletePermanent', paths)
      if (!ok) return null
      const dirs = paths.map(parentOf)
      const snap = snapshot(qc, dirs)
      const set = new Set(paths)
      for (const d of new Set(dirs))
        patchLists(qc, d, (items) => items.filter((it) => !set.has(it.path)))
      try {
        const res = await deletePermanentItems(paths)
        for (const p of paths) invalidateMediaUrl(p)
        invalidate(dirs)
        const msg = t('fileops:toast.deletedPermanent', { count: res.deleted, n: n(res.deleted) })
        toast.success(msg)
        announce(msg)
        return res
      } catch (e) {
        restore(qc, snap)
        fail(e)
        return null
      }
    },
    [qc, invalidate, fail, t, n],
  )
  const dirSizeOf = useCallback<FileOps['dirSize']>(
    async (path) => {
      try {
        const res = await dirSize(path)
        patchLists(qc, parentOf(path), (items) =>
          items.map((it) => (it.path === path ? { ...it, size: res.size } : it)),
        )
        if (res.task) {
          queuedTask(res.task)
          return res
        }
        const msg = t(res.partial ? 'fileops:toast.dirSizePartial' : 'fileops:toast.dirSize', {
          name: path.split('/').pop() ?? path,
          size: formatBytes(res.size, lang),
          files: n(res.files),
          dirs: n(res.dirs),
        })
        toast.success(msg)
        announce(msg)
        return res
      } catch (e) {
        fail(e)
        return null
      }
    },
    [qc, fail, t, n, lang, queuedTask],
  )

  const chmod = useCallback<FileOps['chmod']>(
    async (input) => {
      try {
        const res = await chmodItems(input)
        invalidate(input.paths.map(parentOf))
        const msg = t('fileops:toast.chmod', {
          count: input.paths.length,
          n: n(input.paths.length),
          mode: input.mode,
        })
        toast.success(msg)
        announce(msg)
        return res
      } catch (e) {
        fail(e)
        return null
      }
    },
    [invalidate, fail, t, n],
  )

  const symlink = useCallback<FileOps['symlink']>(
    async (linkPath, target) => {
      const dir = parentOf(linkPath)
      try {
        const res = await createSymlink(linkPath, relativeTarget(dir, target))
        invalidate([dir])
        done(t('fileops:toast.symlinked', { name: res.item?.name ?? linkPath }), res)
        return res
      } catch (e) {
        fail(e)
        return null
      }
    },
    [invalidate, done, fail, t],
  )

  const applyBatchRename = useCallback<FileOps['applyBatchRename']>(
    async (paths, rule) => {
      try {
        const res = await batchRenameApply(paths, rule)
        invalidate(paths.map(parentOf))
        const changed = res.results.length
        done(t('fileops:toast.batchRenamed', { count: changed, n: n(changed) }), res)
        return res
      } catch (e) {
        fail(e)
        throw e
      }
    },
    [invalidate, done, fail, t, n],
  )

  const osDrop = useCallback<FileOps['osDrop']>((files, toDir) => {
    void uploadFromDataTransfer(files, toDir)
  }, [])

  return useMemo(
    () => ({
      copy,
      cut,
      paste,
      transfer,
      rename,
      duplicate,
      remove,
      removePermanent,
      dirSize: dirSizeOf,
      chmod,
      symlink,
      applyBatchRename,
      undo,
      osDrop,
    }),
    [
      copy,
      cut,
      paste,
      transfer,
      rename,
      duplicate,
      remove,
      removePermanent,
      dirSizeOf,
      chmod,
      symlink,
      applyBatchRename,
      undo,
      osDrop,
    ],
  )
}
