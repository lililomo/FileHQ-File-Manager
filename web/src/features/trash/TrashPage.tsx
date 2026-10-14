import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArchiveRestore, ArrowDown, ArrowUp, FolderOutput, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { BrowserContextMenu } from '@/features/browser/ContextMenu'
import { ConflictDialog } from '@/features/file-ops/ConflictDialog'
import { DeletePermanentDialog } from '@/features/file-ops/DeleteDialog'
import { conflictsOf, type Conflict, type ConflictPolicy } from '@/features/file-ops/api'
import { errorMessage } from '@/features/file-ops/useFileOps'
import { QUOTA_QUERY_KEY } from '@/features/quota/api'
import { announce } from '@/lib/announce'
import type { ActionContext } from '@/lib/actions'
import { formatBytes, formatDateTimeFull, formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'
import {
  emptyTrash,
  listTrash,
  purgeTrash,
  restoreTrash,
  TRASH_KINDS,
  trashQueryKey,
  type TrashItem,
  type TrashKind,
  type TrashSort,
} from './api'
import { EmptyTrashDialog, RestoreToDialog } from './TrashDialogs'

type SortState = { field: TrashSort; dir: 'asc' | 'desc' }
type Dialog =
  | { kind: 'restoreTo'; ids: string[] }
  | { kind: 'purge'; ids: string[]; paths: string[] }
  | { kind: 'empty' }
  | {
      kind: 'conflict'
      conflicts: Conflict[]
      resolve: (perItem: Record<string, ConflictPolicy> | null) => void
    }

const COLUMNS: {
  id: 'name' | 'location' | 'size' | 'deleted' | 'expires'
  sort?: TrashSort
  className: string
}[] = [
  { id: 'name', sort: 'name', className: 'min-w-0 flex-1' },
  { id: 'location', sort: 'path', className: 'hidden w-64 shrink-0 md:block' },
  { id: 'size', sort: 'size', className: 'w-24 shrink-0 text-right' },
  { id: 'deleted', sort: 'deleted', className: 'hidden w-40 shrink-0 lg:block' },
  { id: 'expires', sort: 'expires', className: 'hidden w-40 shrink-0 xl:block' },
]

function iconCategory(item: TrashItem): string {
  if (item.isDir) return 'folder'
  return toIconCategory(item.kind === 'file' ? 'text' : item.kind)
}

/** Recycle Bin page (SPEC §8.6, §9.3): filter, search, sort, batch
 *  selection, restore / restore to… / purge / empty with the shared
 *  conflict and confirmation dialogs; right-click uses the "trash" context of
 *  the action registry. */
export function TrashPage() {
  const { t, i18n } = useTranslation(['trash', 'fileops', 'errors', 'files'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const lt = t as unknown as (k: string, o?: Record<string, unknown>) => string
  const qc = useQueryClient()
  const [kind, setKind] = useState<TrashKind | ''>('')
  const [q, setQ] = useState('')
  const [debouncedQ, setDebouncedQ] = useState('')
  const [sort, setSort] = useState<SortState>({ field: 'deleted', dir: 'desc' })
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [active, setActive] = useState<string | null>(null)
  const [anchor, setAnchor] = useState<string | null>(null)
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const [busy, setBusy] = useState(false)
  // extra pages loaded with "Muat lebih banyak", tied to the query they extend
  const [extra, setExtra] = useState<{ key: string; items: TrashItem[] }>({ key: '', items: [] })
  const gridRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const h = window.setTimeout(() => setDebouncedQ(q.trim()), 250)
    return () => window.clearTimeout(h)
  }, [q])

  const opts = { kind, q: debouncedQ, sort: sort.field, dir: sort.dir, limit: 500 }
  const queryKey = trashQueryKey(opts)
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => listTrash(opts, signal),
    placeholderData: (prev) => prev,
  })
  const data = query.data
  const pageKey = `${queryKey.join('|')}|${data?.items.length ?? -1}|${data?.nextCursor ?? ''}`
  const items = useMemo(
    () => [...(data?.items ?? []), ...(extra.key === pageKey ? extra.items : [])],
    [data, extra, pageKey],
  )
  const byId = useMemo(() => new Map(items.map((it) => [it.id, it])), [items])
  // ids of items that disappeared (restored/purged) are ignored, never stored back
  const selectedItems = useMemo(
    () => [...selected].map((id) => byId.get(id)).filter((x): x is TrashItem => !!x),
    [selected, byId],
  )

  const refresh = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ['trash'] })
    void qc.invalidateQueries({ queryKey: ['list'] })
    void qc.invalidateQueries({ queryKey: QUOTA_QUERY_KEY })
  }, [qc])

  const askConflicts = (conflicts: Conflict[]) =>
    new Promise<Record<string, ConflictPolicy> | null>((resolve) => {
      setDialog({
        kind: 'conflict',
        conflicts,
        resolve: (v) => {
          setDialog(null)
          resolve(v)
        },
      })
    })

  const restore = useCallback(
    async (ids: string[], to?: string) => {
      if (ids.length === 0 || busy) return
      setBusy(true)
      try {
        let perItem: Record<string, ConflictPolicy> = {}
        for (;;) {
          try {
            const res = await restoreTrash(ids, { ...(to ? { to } : {}), perItem })
            const ok = res.results.filter((r) => r.status !== 'skipped').length
            const skipped = res.results.length - ok
            let msg = t('trash:toast.restored', { count: ok, n: formatNumber(ok, lang) })
            if (skipped > 0)
              msg += ` ${t('trash:toast.skipped', { count: skipped, n: formatNumber(skipped, lang) })}`
            toast.success(msg)
            announce(msg)
            break
          } catch (e) {
            const conflicts = conflictsOf(e)
            if (!conflicts || conflicts.length === 0) throw e
            const answer = await askConflicts(conflicts)
            if (!answer) break
            perItem = { ...perItem, ...answer }
          }
        }
      } catch (e) {
        const msg = errorMessage(lt, e)
        toast.error(msg)
        announce(msg, true)
      } finally {
        setBusy(false)
        refresh()
      }
    },
    [busy, t, lt, lang, refresh],
  )

  const purge = useCallback(
    async (ids: string[]) => {
      if (ids.length === 0 || busy) return
      setBusy(true)
      try {
        const res = await purgeTrash(ids)
        const msg = t('trash:toast.purged', {
          count: res.deleted,
          n: formatNumber(res.deleted, lang),
        })
        toast.success(msg)
        announce(msg)
      } catch (e) {
        const msg = errorMessage(lt, e)
        toast.error(msg)
        announce(msg, true)
      } finally {
        setBusy(false)
        refresh()
      }
    },
    [busy, t, lt, lang, refresh],
  )

  const empty = useCallback(async () => {
    if (busy) return
    setBusy(true)
    try {
      const res = await emptyTrash()
      const msg = t('trash:toast.emptied', { size: formatBytes(res.bytesFreed, lang) })
      toast.success(msg)
      announce(msg)
    } catch (e) {
      const msg = errorMessage(lt, e)
      toast.error(msg)
      announce(msg, true)
    } finally {
      setBusy(false)
      refresh()
    }
  }, [busy, t, lt, lang, refresh])

  const selectedIds = useMemo(() => selectedItems.map((it) => it.id), [selectedItems])
  const openRestoreTo = useCallback(() => {
    if (selectedIds.length) setDialog({ kind: 'restoreTo', ids: selectedIds })
  }, [selectedIds])
  const openPurge = useCallback(() => {
    if (selectedIds.length)
      setDialog({
        kind: 'purge',
        ids: selectedIds,
        paths: selectedItems.map((it) => it.originalPath),
      })
  }, [selectedIds, selectedItems])

  // --- selection & keyboard ---
  const ids = useMemo(() => items.map((it) => it.id), [items])
  const clickRow = (e: React.MouseEvent, id: string) => {
    setActive(id)
    if (e.shiftKey && anchor) {
      const a = ids.indexOf(anchor)
      const b = ids.indexOf(id)
      if (a >= 0 && b >= 0) {
        const [lo, hi] = a < b ? [a, b] : [b, a]
        setSelected(new Set(ids.slice(lo, hi + 1)))
        return
      }
    }
    if (e.ctrlKey || e.metaKey) {
      setSelected((prev) => {
        const next = new Set(prev)
        if (next.has(id)) next.delete(id)
        else next.add(id)
        return next
      })
    } else {
      setSelected(new Set([id]))
    }
    setAnchor(id)
  }
  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.target instanceof HTMLInputElement) return
    const i = active ? ids.indexOf(active) : -1
    const move = (to: number) => {
      const id = ids[Math.max(0, Math.min(ids.length - 1, to))]
      if (!id) return
      setActive(id)
      if (e.shiftKey && anchor) {
        const a = ids.indexOf(anchor)
        const b = ids.indexOf(id)
        const [lo, hi] = a < b ? [a, b] : [b, a]
        setSelected(new Set(ids.slice(lo, hi + 1)))
      } else {
        setSelected(new Set([id]))
        setAnchor(id)
      }
      gridRef.current
        ?.querySelector<HTMLElement>(`[data-id="${CSS.escape(id)}"]`)
        ?.scrollIntoView({ block: 'nearest' })
    }
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        move(i + 1)
        break
      case 'ArrowUp':
        e.preventDefault()
        move(i - 1)
        break
      case 'Home':
        e.preventDefault()
        move(0)
        break
      case 'End':
        e.preventDefault()
        move(ids.length - 1)
        break
      case ' ':
        if (active) {
          e.preventDefault()
          toggle(active)
        }
        break
      case 'a':
      case 'A':
        if (e.ctrlKey || e.metaKey) {
          e.preventDefault()
          setSelected(new Set(ids))
        }
        break
      case 'Escape':
        setSelected(new Set())
        break
      case 'Enter':
        if (selectedIds.length) {
          e.preventDefault()
          void restore(selectedIds)
        }
        break
      case 'Delete':
        if (selectedIds.length) {
          e.preventDefault()
          openPurge()
        }
        break
      default:
    }
  }
  const allSelected = ids.length > 0 && selectedIds.length === ids.length
  const someSelected = selectedIds.length > 0 && !allSelected

  const actionCtx = useMemo<ActionContext | null>(() => {
    if (selectedItems.length === 0) return null
    const noop = () => {}
    return {
      path: '/',
      selection: [],
      selectionCount: selectedItems.length,
      total: items.length,
      clipboardCount: 0,
      canUndo: false,
      navigate: noop,
      refresh,
      openNew: noop,
      openTab: noop,
      closeTab: noop,
      togglePreview: noop,
      toggleTheme: noop,
      selectAll: () => setSelected(new Set(ids)),
      invertSelection: noop,
      clearSelection: () => setSelected(new Set()),
      openHelp: noop,
      openColumns: noop,
      openProperties: noop,
      openFolderProperties: noop,
      quickLook: noop,
      focusFilter: noop,
      open: noop,
      openInNewTab: noop,
      copy: noop,
      cut: noop,
      paste: noop,
      duplicate: noop,
      rename: noop,
      moveTo: noop,
      copyTo: noop,
      deleteItems: noop,
      deletePermanent: noop,
      batchRename: noop,
      permissions: noop,
      symlinkTo: noop,
      copyPath: noop,
      undo: noop,
      restore: () => void restore(selectedIds),
      restoreTo: openRestoreTo,
      purge: openPurge,
    }
  }, [selectedItems, items.length, ids, refresh, restore, selectedIds, openRestoreTo, openPurge])

  const headerSort = (field: TrashSort) =>
    setSort((s) =>
      s.field === field
        ? { field, dir: s.dir === 'asc' ? 'desc' : 'asc' }
        : { field, dir: field === 'deleted' ? 'desc' : 'asc' },
    )

  const retention = data?.retentionDays ?? 0
  const total = data?.total ?? 0
  const totalBytes = data?.totalBytes ?? 0
  const selectedBytes = selectedItems.reduce((n, it) => n + it.size, 0)

  return (
    <div className="flex h-full flex-col" data-testid="trash-page">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold">{t('trash:title')}</h1>
        <p
          className="w-full text-xs text-text-muted sm:ml-2 sm:w-auto"
          data-testid="trash-retention"
        >
          {retention > 0
            ? t('trash:retention', { count: retention, n: formatNumber(retention, lang) })
            : t('trash:retentionNone')}
        </p>
      </header>
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2">
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t('trash:search')}
          aria-label={t('trash:search')}
          className="h-8 w-56"
          data-testid="trash-search"
        />
        <Select
          value={kind || 'all'}
          onValueChange={(v) => setKind(v === 'all' ? '' : (v as TrashKind))}
        >
          <SelectTrigger
            className="h-8 w-40"
            aria-label={t('trash:kind.label')}
            data-testid="trash-kind"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('trash:kind.all')}</SelectItem>
            {TRASH_KINDS.map((k) => (
              <SelectItem key={k} value={k}>
                {t(`trash:kind.${k}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className="ml-auto flex flex-wrap items-center gap-1">
          <Button
            size="sm"
            variant="outline"
            disabled={selectedIds.length === 0 || busy}
            onClick={() => void restore(selectedIds)}
            data-testid="trash-restore"
          >
            <ArchiveRestore aria-hidden="true" />
            {t('trash:actions.restore')}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={selectedIds.length === 0 || busy}
            onClick={openRestoreTo}
            data-testid="trash-restore-to"
          >
            <FolderOutput aria-hidden="true" />
            {t('trash:actions.restoreTo')}
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="text-danger"
            disabled={selectedIds.length === 0 || busy}
            onClick={openPurge}
            data-testid="trash-purge"
          >
            <Trash2 aria-hidden="true" />
            {t('trash:actions.purge')}
          </Button>
          <Button
            size="sm"
            variant="danger"
            disabled={total === 0 || busy}
            onClick={() => setDialog({ kind: 'empty' })}
            data-testid="trash-empty"
          >
            {t('trash:actions.empty')}
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {query.isPending ? (
          <div className="space-y-2 p-4" aria-busy="true">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-2/3" />
          </div>
        ) : query.isError ? (
          <EmptyState
            title={t('trash:error.title')}
            description={errorMessage(lt, query.error)}
            action={
              <Button variant="outline" onClick={() => void query.refetch()}>
                {t('trash:error.retry')}
              </Button>
            }
          />
        ) : items.length === 0 ? (
          <EmptyState
            icon={Trash2}
            title={debouncedQ || kind ? t('trash:empty.noMatch') : t('trash:empty.title')}
            description={debouncedQ || kind ? '' : t('trash:empty.description')}
            className="h-full"
          />
        ) : (
          <BrowserContextMenu
            ctx={actionCtx}
            context="trash"
            viewMenu={null}
            sortMenu={null}
            columnsMenu={null}
            templates={[]}
          >
            <div
              ref={gridRef}
              role="grid"
              aria-label={t('trash:grid')}
              aria-multiselectable="true"
              aria-rowcount={items.length + 1}
              aria-activedescendant={active ? `trash-row-${active}` : undefined}
              tabIndex={0}
              onKeyDown={onKeyDown}
              className="min-w-full text-sm outline-none focus-visible:ring-2 focus-visible:ring-accent"
              data-testid="trash-grid"
            >
              <div
                role="row"
                className="sticky top-0 z-10 flex h-8 items-center gap-2 border-b border-border bg-surface px-3 text-xs text-text-muted"
              >
                <div role="columnheader" className="w-6 shrink-0">
                  <Checkbox
                    checked={allSelected ? true : someSelected ? 'indeterminate' : false}
                    onCheckedChange={(v) => setSelected(v ? new Set(ids) : new Set())}
                    aria-label={t('trash:selectAll')}
                    data-testid="trash-select-all"
                  />
                </div>
                {COLUMNS.map((c) => (
                  <div
                    key={c.id}
                    role="columnheader"
                    className={c.className}
                    aria-sort={
                      sort.field === c.sort
                        ? sort.dir === 'asc'
                          ? 'ascending'
                          : 'descending'
                        : 'none'
                    }
                  >
                    <button
                      type="button"
                      className={cn(
                        'inline-flex items-center gap-1 hover:text-text',
                        c.id === 'size' && 'w-full justify-end',
                      )}
                      onClick={() => c.sort && headerSort(c.sort)}
                      data-testid={`trash-col-${c.id}`}
                    >
                      {t(`trash:columns.${c.id}`)}
                      {sort.field === c.sort ? (
                        sort.dir === 'asc' ? (
                          <ArrowUp className="size-3" aria-hidden="true" />
                        ) : (
                          <ArrowDown className="size-3" aria-hidden="true" />
                        )
                      ) : null}
                    </button>
                  </div>
                ))}
              </div>
              <div role="rowgroup" data-testid="trash-body">
                {items.map((it) => {
                  const isSel = selected.has(it.id)
                  return (
                    <div
                      key={it.id}
                      id={`trash-row-${it.id}`}
                      role="row"
                      aria-selected={isSel}
                      data-id={it.id}
                      data-name={it.name}
                      data-testid="trash-row"
                      className={cn(
                        'flex h-9 items-center gap-2 border-b border-border/60 px-3',
                        isSel ? 'bg-accent-subtle' : 'hover:bg-surface-2',
                        active === it.id && 'outline outline-1 -outline-offset-1 outline-accent',
                      )}
                      onClick={(e) => clickRow(e, it.id)}
                      onContextMenu={() => {
                        if (!isSel) {
                          setSelected(new Set([it.id]))
                          setActive(it.id)
                          setAnchor(it.id)
                        }
                      }}
                      onDoubleClick={() => void restore([it.id])}
                    >
                      <div
                        role="gridcell"
                        className="w-6 shrink-0"
                        onClick={(e) => e.stopPropagation()}
                      >
                        <Checkbox
                          checked={isSel}
                          onCheckedChange={() => toggle(it.id)}
                          aria-label={t('trash:selectRow', { name: it.name })}
                        />
                      </div>
                      <div role="gridcell" className="flex min-w-0 flex-1 items-center gap-2">
                        <FileIcon category={iconCategory(it)} size={18} className="shrink-0" />
                        <span className="truncate" title={it.name}>
                          {it.name}
                        </span>
                      </div>
                      <div
                        role="gridcell"
                        className="hidden w-64 shrink-0 truncate font-mono text-xs text-text-muted md:block"
                        title={it.originalPath}
                      >
                        {it.originalPath.split('/').slice(0, -1).join('/') || '/'}
                      </div>
                      <div role="gridcell" className="w-24 shrink-0 text-right tabular">
                        {formatBytes(it.size, lang)}
                      </div>
                      <div
                        role="gridcell"
                        className="hidden w-40 shrink-0 truncate text-xs text-text-muted lg:block"
                      >
                        {formatDateTimeFull(it.deletedAt, lang)}
                      </div>
                      <div
                        role="gridcell"
                        className="hidden w-40 shrink-0 truncate text-xs text-text-muted xl:block"
                      >
                        {it.expiresAt ? formatDateTimeFull(it.expiresAt, lang) : t('trash:never')}
                      </div>
                    </div>
                  )
                })}
              </div>
              {data?.nextCursor ? (
                <div className="p-3">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      void listTrash({ ...opts, cursor: data.nextCursor }).then((r) =>
                        setExtra((x) => ({
                          key: pageKey,
                          items: [...(x.key === pageKey ? x.items : []), ...r.items],
                        })),
                      )
                    }
                    data-testid="trash-load-more"
                  >
                    {t('trash:actions.loadMore')}
                  </Button>
                </div>
              ) : null}
            </div>
          </BrowserContextMenu>
        )}
      </div>
      <div
        className="flex h-7 shrink-0 items-center gap-3 border-t border-border bg-surface px-3 text-xs text-text-muted tabular"
        role="status"
        data-testid="trash-status"
      >
        <span data-testid="trash-status-items">
          {t('trash:status.items', {
            count: total,
            n: formatNumber(total, lang),
            size: formatBytes(totalBytes, lang),
          })}
        </span>
        {selectedIds.length > 0 ? (
          <span data-testid="trash-status-selected">
            {t('trash:status.selected', {
              count: selectedIds.length,
              n: formatNumber(selectedIds.length, lang),
            })}
            {` (${formatBytes(selectedBytes, lang)})`}
          </span>
        ) : null}
      </div>
      {dialog?.kind === 'restoreTo' ? (
        <RestoreToDialog
          count={dialog.ids.length}
          onClose={() => setDialog(null)}
          onSubmit={(to) => {
            setDialog(null)
            void restore(dialog.ids, to)
          }}
        />
      ) : null}
      {dialog?.kind === 'purge' ? (
        <DeletePermanentDialog
          paths={dialog.paths}
          onResolve={(ok) => {
            setDialog(null)
            if (ok) void purge(dialog.ids)
          }}
        />
      ) : null}
      {dialog?.kind === 'empty' ? (
        <EmptyTrashDialog
          count={total}
          bytes={totalBytes}
          onResolve={(ok) => {
            setDialog(null)
            if (ok) void empty()
          }}
        />
      ) : null}
      {dialog?.kind === 'conflict' ? (
        <ConflictDialog op="move" conflicts={dialog.conflicts} onResolve={dialog.resolve} />
      ) : null}
    </div>
  )
}
