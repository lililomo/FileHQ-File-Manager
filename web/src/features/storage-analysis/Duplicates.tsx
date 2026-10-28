import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useVirtualizer } from '@tanstack/react-virtual'
import { Copy, FolderOpen, FolderSearch, Link2, Search, Share2, Trash2, X } from 'lucide-react'
import { useEffect, useMemo, useReducer, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Progress } from '@/components/ui/progress'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Skeleton } from '@/components/ui/skeleton'
import { pathToSplat } from '@/features/browser/path'
import { FolderPicker } from '@/features/file-ops/FolderPicker'
import { showUndoToast } from '@/features/file-ops/undoToast'
import { errorMessage, useFileOps } from '@/features/file-ops/useFileOps'
import { QUOTA_QUERY_KEY } from '@/features/quota/api'
import { cancelTask, getTask, isActive } from '@/features/tasks/api'
import { ApiError } from '@/lib/api'
import { announce } from '@/lib/announce'
import { formatBytes, formatDateTimeFull, formatNumber, formatRelativeTime } from '@/lib/format'
import { useTasksStore } from '@/stores/tasks'
import {
  DUPLICATE_TASK_TYPE,
  DUPLICATES_QUERY_KEY,
  getDuplicates,
  replaceWithSymlinks,
  startDuplicateScan,
  STORAGE_QUERY_KEY,
  withoutPaths,
  type DuplicateGroup,
  type DuplicateReport,
} from './api'
import { dupReducer, dupTotals, type DupAction, type GroupSelection } from './dupSelection'

type LooseT = (k: string, o?: Record<string, unknown>) => string

/** Groups rendered without virtualization up to this count. */
const VIRTUALIZE_AFTER = 50

function dirOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i <= 0 ? '/' : path.slice(0, i)
}

/** "Duplikat" tab of `/storage` (SPEC §8.7, Step 23c): scan a folder as a
 *  `duplicate_finder` task, pick the copy to keep per group, send the rest to
 *  the Recycle Bin (undoable) or replace them with symlinks. */
export function Duplicates() {
  const { t, i18n } = useTranslation(['meta', 'files', 'fileops', 'errors', 'tasks'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const ops = useFileOps()
  const navigate = useNavigate()
  const [scope, setScope] = useState('/')
  const [picking, setPicking] = useState(false)
  const [confirm, setConfirm] = useState<'delete' | 'symlink' | null>(null)
  const [busy, setBusy] = useState(false)
  const [scanError, setScanError] = useState<string | null>(null)
  const [sel, dispatch] = useReducer(dupReducer, {})

  const query = useQuery({
    queryKey: DUPLICATES_QUERY_KEY,
    queryFn: ({ signal }) => getDuplicates(signal),
    staleTime: 30_000,
  })
  const report = query.data ?? null
  const groups = useMemo(() => report?.groups ?? [], [report])
  useEffect(() => dispatch({ type: 'init', groups }), [groups])
  const totals = useMemo(() => dupTotals(groups, sel), [groups, sel])

  const running = useTasksStore((s) =>
    Object.values(s.tasks).find((x) => x.type === DUPLICATE_TASK_TYPE && isActive(x)),
  )
  // a scan that reaches a final state refreshes the report or reports why
  useEffect(
    () =>
      useTasksStore.subscribe((state, prev) => {
        for (const task of Object.values(state.tasks)) {
          if (task.type !== DUPLICATE_TASK_TYPE || isActive(task)) continue
          const before = prev.tasks[task.id]
          if (!before || !isActive(before)) continue
          if (task.state === 'done') {
            setScanError(null)
            void qc.invalidateQueries({ queryKey: DUPLICATES_QUERY_KEY })
            announce(t('meta:storage.dup.scanDone'))
          } else if (task.state === 'cancelled') {
            announce(t('meta:storage.dup.scanCancelled'))
          } else {
            const msg =
              task.error?.code === 'TIMEOUT'
                ? t('meta:storage.dup.timeout')
                : (task.error?.message ?? task.error?.code ?? '')
            setScanError(msg)
            announce(t('meta:storage.dup.scanFailed', { message: msg }), true)
          }
        }
      }),
    [qc, t],
  )

  const failMsg = (e: unknown) => {
    if (e instanceof ApiError && typeof e.details.reason === 'string') {
      const specific = lt(`meta:storage.dup.error.${e.details.reason}`, { defaultValue: '' })
      if (specific) return specific
    }
    return errorMessage(lt, e)
  }

  const start = useMutation({
    mutationFn: () => startDuplicateScan(scope),
    onSuccess: ({ task }) => {
      setScanError(null)
      useTasksStore.getState().upsert(task)
      announce(t('meta:storage.dup.scanStarted', { path: scope }))
    },
    onError: (e) => {
      if (e instanceof ApiError && e.details.reason === 'already_running') {
        const id = e.details.taskId
        if (typeof id === 'string')
          void getTask(id)
            .then(({ task }) => useTasksStore.getState().upsert(task))
            .catch(() => undefined)
        toast.info(t('meta:storage.dup.error.already_running'))
        return
      }
      const msg = failMsg(e)
      toast.error(msg)
      announce(msg, true)
    },
  })

  const afterRemoval = (removed: string[]) => {
    qc.setQueryData<DuplicateReport | null>(DUPLICATES_QUERY_KEY, (old) =>
      old ? withoutPaths(old, removed) : old,
    )
    void qc.invalidateQueries({ queryKey: STORAGE_QUERY_KEY })
    void qc.invalidateQueries({ queryKey: QUOTA_QUERY_KEY })
  }

  const runConfirmed = async (kind: 'delete' | 'symlink') => {
    setConfirm(null)
    const paths = totals.paths
    if (paths.length === 0) return
    setBusy(true)
    try {
      if (kind === 'delete') {
        const res = await ops.remove(paths, { confirm: false })
        if (res) afterRemoval(res.results.map((r) => r.path))
        return
      }
      const res = await replaceWithSymlinks(totals.links)
      const linked = res.results.filter((r) => r.status === 'ok').map((r) => r.path)
      afterRemoval(res.results.map((r) => r.path))
      for (const d of new Set(paths.map(dirOf)))
        void qc.invalidateQueries({ queryKey: ['list', d] })
      const msg = t('meta:storage.dup.linked', {
        count: linked.length,
        n: formatNumber(linked.length, lang),
      })
      announce(msg)
      if (res.undoToken) showUndoToast(msg, res.undoToken, () => void ops.undo())
      else toast.success(msg)
    } catch (e) {
      const msg = failMsg(e)
      toast.error(msg)
      announce(msg, true)
    } finally {
      setBusy(false)
    }
  }

  const openLocation = (path: string) => {
    const dir = dirOf(path)
    void navigate({
      to: '/files/$',
      params: { _splat: pathToSplat(dir) },
      search: { select: path.slice(path.lastIndexOf('/') + 1) },
    })
  }

  const percent = running?.progress.total ? Math.round(running.progress.percent) : null

  return (
    <div className="space-y-4" data-testid="duplicates">
      <section
        className="flex flex-wrap items-center gap-2"
        aria-label={t('meta:storage.dup.scope')}
      >
        <span className="text-sm text-text-muted">{t('meta:storage.dup.scope')}</span>
        <code className="rounded bg-surface-2 px-2 py-1 text-xs" data-testid="dup-scope">
          {scope}
        </code>
        <Button
          size="sm"
          variant="outline"
          onClick={() => setPicking(true)}
          disabled={!!running}
          data-testid="dup-pick"
        >
          <FolderSearch aria-hidden="true" />
          {t('meta:storage.dup.change')}
        </Button>
        <Button
          size="sm"
          variant="primary"
          onClick={() => start.mutate()}
          disabled={!!running || start.isPending}
          data-testid="dup-start"
        >
          <Search aria-hidden="true" />
          {t('meta:storage.dup.start')}
        </Button>
      </section>

      {running ? (
        <div
          className="space-y-2 rounded-md border border-border bg-surface p-3"
          role="status"
          data-testid="dup-running"
        >
          <div className="flex items-center gap-2 text-sm">
            <span className="flex-1">
              {t('meta:storage.dup.running')}
              {running.progress.total > 0
                ? ` — ${lt('tasks:meta.items', {
                    done: formatNumber(running.progress.done, lang),
                    total: formatNumber(running.progress.total, lang),
                  })}`
                : ''}
            </span>
            <Button
              size="sm"
              variant="outline"
              onClick={() => void cancelTask(running.id).catch((e) => toast.error(failMsg(e)))}
              disabled={!running.cancelable || running.cancelRequested}
              data-testid="dup-cancel"
            >
              <X aria-hidden="true" />
              {t('meta:storage.dup.cancel')}
            </Button>
          </div>
          <Progress
            value={percent ?? 0}
            aria-label={t('meta:storage.dup.running')}
            className={percent === null ? 'animate-pulse' : undefined}
          />
        </div>
      ) : null}
      {scanError ? (
        <p
          className="rounded-md border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger"
          role="alert"
          data-testid="dup-error"
        >
          {t('meta:storage.dup.scanFailed', { message: scanError })}
        </p>
      ) : null}

      {query.isPending ? (
        <div className="space-y-2" aria-busy="true">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-32 w-full" />
        </div>
      ) : query.isError ? (
        <EmptyState
          icon={Copy}
          title={t('files:error.title')}
          description={failMsg(query.error)}
          action={
            <Button variant="outline" onClick={() => void query.refetch()}>
              {t('files:error.retry')}
            </Button>
          }
        />
      ) : !report ? (
        <EmptyState
          icon={Copy}
          title={t('meta:storage.dup.none')}
          description={t('meta:storage.dup.noneHint')}
        />
      ) : (
        <>
          <section
            className="flex flex-wrap items-baseline gap-x-4 gap-y-1 rounded-md border border-border bg-surface p-3 text-sm"
            data-testid="dup-summary"
          >
            <span data-testid="dup-groups" data-value={groups.length}>
              {t('meta:storage.dup.groups', {
                count: groups.length,
                n: formatNumber(groups.length, lang),
              })}
            </span>
            <span data-testid="dup-reclaimable" data-value={report.reclaimable}>
              {t('meta:storage.dup.reclaimable', {
                size: formatBytes(report.reclaimable, lang),
              })}
            </span>
            <span className="text-text-muted" data-testid="dup-scanned" data-value={report.scanned}>
              {t('meta:storage.dup.scanned', {
                n: formatNumber(report.scanned, lang),
                path: report.path,
              })}
            </span>
            <span
              className="text-xs text-text-muted"
              title={formatDateTimeFull(report.generatedAt, lang)}
            >
              {t('meta:storage.generatedAt', { at: formatRelativeTime(report.generatedAt, lang) })}{' '}
              · {t('meta:storage.dup.kept24h')}
            </span>
            {report.truncated ? (
              <span className="w-full text-xs text-warning" role="note">
                {t('meta:storage.dup.truncated', {
                  n: formatNumber(groups.length, lang),
                  total: formatNumber(report.totalGroups, lang),
                })}
              </span>
            ) : null}
          </section>
          {groups.length === 0 ? (
            <EmptyState icon={Copy} title={t('meta:storage.dup.empty')} />
          ) : (
            <>
              <GroupList
                groups={groups}
                sel={sel}
                dispatch={dispatch}
                onOpenLocation={openLocation}
                lang={lang}
              />
              <div
                className="sticky bottom-0 flex flex-wrap items-center gap-2 border-t border-border bg-bg py-3"
                data-testid="dup-footer"
              >
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => dispatch({ type: 'selectAll', groups })}
                  data-testid="dup-select-all"
                >
                  {t('meta:storage.dup.selectAll')}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => dispatch({ type: 'clearAll' })}
                  data-testid="dup-select-none"
                >
                  {t('meta:storage.dup.selectNone')}
                </Button>
                <span className="ml-auto" />
                <Button
                  size="sm"
                  variant="outline"
                  disabled={totals.count === 0 || busy}
                  onClick={() => setConfirm('symlink')}
                  data-testid="dup-symlink"
                >
                  <Link2 aria-hidden="true" />
                  {t('meta:storage.dup.symlink')}
                </Button>
                <Button
                  size="sm"
                  variant="danger"
                  disabled={totals.count === 0 || busy}
                  onClick={() => setConfirm('delete')}
                  data-testid="dup-delete"
                >
                  <Trash2 aria-hidden="true" />
                  {t('meta:storage.dup.delete', {
                    count: totals.count,
                    n: formatNumber(totals.count, lang),
                    size: formatBytes(totals.bytes, lang),
                  })}
                </Button>
              </div>
            </>
          )}
        </>
      )}

      {picking ? (
        <ScopeDialog
          initial={scope}
          onClose={() => setPicking(false)}
          onChoose={(p) => {
            setScope(p)
            setPicking(false)
          }}
        />
      ) : null}
      <AlertDialog
        open={confirm !== null}
        onOpenChange={(o) => (!o ? setConfirm(null) : undefined)}
      >
        <AlertDialogContent data-testid="dup-confirm">
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirm === 'symlink'
                ? t('meta:storage.dup.confirmSymlinkTitle', {
                    count: totals.count,
                    n: formatNumber(totals.count, lang),
                  })
                : t('meta:storage.dup.confirmDeleteTitle', {
                    count: totals.count,
                    n: formatNumber(totals.count, lang),
                  })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirm === 'symlink'
                ? t('meta:storage.dup.confirmSymlink', { size: formatBytes(totals.bytes, lang) })
                : t('meta:storage.dup.confirmDelete', { size: formatBytes(totals.bytes, lang) })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          {totals.shared.length > 0 ? (
            <div
              className="space-y-1 rounded-md border border-warning/40 bg-warning/10 p-2 text-xs"
              role="alert"
              data-testid="dup-shared-warning"
            >
              <p className="font-medium text-warning">
                {t('meta:storage.dup.sharedWarning', {
                  count: totals.shared.length,
                  n: formatNumber(totals.shared.length, lang),
                })}
              </p>
              <ul className="max-h-32 list-disc overflow-auto pl-4 font-mono">
                {totals.shared.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            </div>
          ) : null}
          <AlertDialogFooter>
            <AlertDialogCancel>{t('fileops:cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => (confirm ? void runConfirmed(confirm) : undefined)}
              data-testid="dup-confirm-ok"
            >
              {confirm === 'symlink'
                ? t('meta:storage.dup.symlink')
                : t('meta:storage.dup.confirmDeleteOk')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function ScopeDialog({
  initial,
  onClose,
  onChoose,
}: {
  initial: string
  onClose: () => void
  onChoose: (path: string) => void
}) {
  const { t } = useTranslation(['meta', 'fileops'])
  const [value, setValue] = useState(initial)
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent className="sm:max-w-md" data-testid="dup-scope-dialog">
        <DialogHeader>
          <DialogTitle>{t('meta:storage.dup.scopeTitle')}</DialogTitle>
          <DialogDescription>{t('meta:storage.dup.scopeHint')}</DialogDescription>
        </DialogHeader>
        <FolderPicker value={value} onChange={setValue} onActivate={onChoose} autoFocus />
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('fileops:cancel')}
          </Button>
          <Button
            type="button"
            variant="primary"
            onClick={() => onChoose(value)}
            data-testid="dup-scope-ok"
          >
            {t('meta:storage.dup.scopeChoose')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

interface GroupListProps {
  groups: DuplicateGroup[]
  sel: Record<string, GroupSelection>
  dispatch: (a: DupAction) => void
  onOpenLocation: (path: string) => void
  lang: string
}

function GroupList(props: GroupListProps) {
  return props.groups.length > VIRTUALIZE_AFTER ? (
    <VirtualGroupList {...props} />
  ) : (
    <ul className="space-y-3" data-testid="dup-list">
      {props.groups.map((g) => (
        <li key={g.hash}>
          <GroupCard group={g} {...props} />
        </li>
      ))}
    </ul>
  )
}

function VirtualGroupList(props: GroupListProps) {
  const parent = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: props.groups.length,
    getScrollElement: () => parent.current,
    estimateSize: (i) => 56 + (props.groups[i]?.paths.length ?? 2) * 40,
    overscan: 4,
  })
  return (
    <div ref={parent} className="max-h-[70vh] overflow-auto" data-testid="dup-list">
      <ul className="relative" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((v) => {
          const g = props.groups[v.index]!
          return (
            <li
              key={g.hash}
              data-index={v.index}
              ref={virtualizer.measureElement}
              className="absolute inset-x-0 pb-3"
              style={{ transform: `translateY(${v.start}px)` }}
            >
              <GroupCard group={g} {...props} />
            </li>
          )
        })}
      </ul>
    </div>
  )
}

function GroupCard({
  group,
  sel,
  dispatch,
  onOpenLocation,
  lang,
}: GroupListProps & { group: DuplicateGroup }) {
  const { t } = useTranslation(['meta'])
  const s = sel[group.hash]
  const keep = s?.keep ?? group.paths[0]?.path ?? ''
  const marked = new Set(s?.remove ?? [])
  const title = t('meta:storage.dup.groupTitle', {
    count: group.paths.length,
    n: formatNumber(group.paths.length, lang),
    size: formatBytes(group.size, lang),
  })
  return (
    <div
      className="rounded-md border border-border bg-surface"
      role="group"
      aria-label={`${group.paths[0]?.name ?? ''} — ${title}`}
      data-testid="dup-group"
      data-hash={group.hash}
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2 text-sm">
        <span className="min-w-0 flex-1 truncate font-medium">{group.paths[0]?.name}</span>
        <span className="text-xs text-text-muted">{title}</span>
        <span className="text-xs text-text-muted">
          {t('meta:storage.dup.reclaimable', { size: formatBytes(group.reclaimable, lang) })}
        </span>
      </div>
      <RadioGroup
        value={keep}
        onValueChange={(path) => dispatch({ type: 'keep', hash: group.hash, path, group })}
        className="gap-0"
        aria-label={t('meta:storage.dup.keepGroup', { name: group.paths[0]?.name ?? '' })}
      >
        {group.paths.map((f) => {
          const kept = f.path === keep
          const id = `dup-${group.hash.slice(0, 12)}-${encodeURIComponent(f.path)}`
          return (
            <div
              key={f.path}
              className="flex items-center gap-3 border-b border-border/60 px-3 py-1.5 text-sm last:border-b-0"
              data-testid="dup-row"
              data-path={f.path}
              data-kept={kept}
            >
              <label className="flex items-center gap-1.5 text-xs" htmlFor={`${id}-keep`}>
                <RadioGroupItem
                  id={`${id}-keep`}
                  value={f.path}
                  aria-label={t('meta:storage.dup.keepOne', { path: f.path })}
                  data-testid="dup-keep"
                />
                <span aria-hidden="true">{t('meta:storage.dup.keep')}</span>
              </label>
              <label className="flex items-center gap-1.5 text-xs" htmlFor={`${id}-del`}>
                <Checkbox
                  id={`${id}-del`}
                  checked={!kept && marked.has(f.path)}
                  disabled={kept}
                  onCheckedChange={(v) =>
                    dispatch({ type: 'toggle', hash: group.hash, path: f.path, on: v === true })
                  }
                  aria-label={t('meta:storage.dup.removeOne', { path: f.path })}
                  data-testid="dup-remove"
                />
                <span aria-hidden="true">{t('meta:storage.dup.remove')}</span>
              </label>
              <span className="min-w-0 flex-1 truncate font-mono text-xs" title={f.path}>
                {f.path}
              </span>
              {f.shared ? (
                <Badge variant="outline" className="gap-1" data-testid="dup-shared">
                  <Share2 aria-hidden="true" />
                  {t('meta:storage.dup.shared')}
                </Badge>
              ) : null}
              <span
                className="hidden w-28 text-xs text-text-muted md:block"
                title={formatDateTimeFull(f.mtime, lang)}
              >
                {formatRelativeTime(f.mtime, lang)}
              </span>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`${t('meta:storage.openLocation')}: ${f.path}`}
                onClick={() => onOpenLocation(f.path)}
                data-testid="dup-open"
              >
                <FolderOpen aria-hidden="true" />
              </Button>
            </div>
          )
        })}
      </RadioGroup>
    </div>
  )
}
