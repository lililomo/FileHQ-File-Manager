import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ChevronRight, FolderOpen, PieChart, RotateCw, Trash2 } from 'lucide-react'
import { useCallback, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { taskOf } from '@/features/tasks/api'
import { taskLabel } from '@/features/tasks/taskLabel'
import { useTasksStore } from '@/stores/tasks'
import { EmptyState } from '@/components/EmptyState'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { pathToSplat } from '@/features/browser/path'
import { useFileOps, errorMessage } from '@/features/file-ops/useFileOps'
import { QUOTA_QUERY_KEY } from '@/features/quota/api'
import { announce } from '@/lib/announce'
import { formatBytes, formatDateTimeFull, formatNumber, formatRelativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import {
  getStorageAnalysis,
  recomputeStorageAnalysis,
  STORAGE_QUERY_KEY,
  TYPE_CLASSES,
  type AnalysisNode,
} from './api'
import { Treemap } from './Treemap'
import { Duplicates } from './Duplicates'
import type { StorageTab } from './dupSelection'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { FileOpsDialogs } from '@/features/file-ops/FileOpsDialogs'

function findNode(root: AnalysisNode, path: string): AnalysisNode | null {
  if (root.path === path && root.kind === 'dir') return root
  for (const c of root.children ?? []) {
    if (c.kind !== 'dir') continue
    const f = findNode(c, path)
    if (f) return f
  }
  return null
}

/** Width of a container that may mount after the first render (callback ref
 *  + ResizeObserver). */
function useWidth() {
  const [width, setWidth] = useState(0)
  const obs = useRef<ResizeObserver | null>(null)
  const ref = useCallback((el: HTMLDivElement | null) => {
    obs.current?.disconnect()
    obs.current = null
    if (!el) return
    setWidth(Math.floor(el.getBoundingClientRect().width))
    const ro = new ResizeObserver(([entry]) => {
      if (entry) setWidth(Math.floor(entry.contentRect.width))
    })
    ro.observe(el)
    obs.current = ro
  }, [])
  return [ref, width] as const
}

/** `/storage` (SPEC §8.7): tabs "Ringkasan" (summary) and "Duplikat"
 *  (Step 23c), deep link `?tab=duplicates`. */
export function StoragePage({
  tab = 'summary',
  onTabChange,
}: {
  tab?: StorageTab
  onTabChange?: (tab: StorageTab) => void
}) {
  const { t } = useTranslation(['meta'])
  const ops = useFileOps()
  return (
    <div className="flex h-full flex-col overflow-auto" data-testid="storage-page">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold">{t('meta:storage.title')}</h1>
        <p className="w-full text-xs text-text-muted sm:ml-2 sm:w-auto">
          {t('meta:storage.subtitle')}
        </p>
      </header>
      <Tabs
        value={tab}
        onValueChange={(v) => onTabChange?.(v === 'duplicates' ? 'duplicates' : 'summary')}
        className="gap-0"
      >
        <TabsList variant="line" className="mx-4 mt-2" aria-label={t('meta:storage.tabs.label')}>
          <TabsTrigger value="summary" data-testid="storage-tab-summary">
            {t('meta:storage.tabs.summary')}
          </TabsTrigger>
          <TabsTrigger value="duplicates" data-testid="storage-tab-duplicates">
            {t('meta:storage.tabs.duplicates')}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="summary">
          <StorageSummary />
        </TabsContent>
        <TabsContent value="duplicates" className="p-4">
          <Duplicates />
        </TabsContent>
      </Tabs>
      {/* delete confirmations of both tabs (the browser mounts its own) */}
      <FileOpsDialogs ops={ops} />
    </div>
  )
}

/** "Ringkasan": squarified treemap (depth ≤ 3, drill-down), the 50 largest
 *  files, totals per type, Recycle Bin / versions and "Hitung ulang". */
function StorageSummary() {
  const { t, i18n } = useTranslation(['meta', 'files', 'fileops', 'errors'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const lt = t as unknown as (k: string, o?: Record<string, unknown>) => string
  const navigate = useNavigate()
  const qc = useQueryClient()
  const ops = useFileOps()
  const query = useQuery({
    queryKey: STORAGE_QUERY_KEY,
    queryFn: ({ signal }) => getStorageAnalysis(signal),
    staleTime: 60_000,
  })
  const recompute = useMutation({
    mutationFn: recomputeStorageAnalysis,
    onSuccess: (data) => {
      qc.setQueryData(STORAGE_QUERY_KEY, data)
      void qc.invalidateQueries({ queryKey: QUOTA_QUERY_KEY })
      const task = taskOf(data)
      if (task) {
        // partial walk: a task completes it and `task.done` refetches the page
        useTasksStore.getState().upsert(task)
        useTasksStore.getState().setPanel(true)
        const msg = lt('tasks:toast.queued', { title: taskLabel(task, lt, lang) })
        toast.info(msg)
        announce(msg)
        return
      }
      announce(t('meta:storage.generatedAt', { at: formatDateTimeFull(data.generatedAt, lang) }))
    },
    onError: (e) => toast.error(errorMessage(lt, e)),
  })
  const [cursor, setCursor] = useState('/')
  const [ref, width] = useWidth()
  const data = query.data
  const node = useMemo(
    () => (data ? (findNode(data.tree, cursor) ?? data.tree) : null),
    [data, cursor],
  )
  const crumbs = useMemo(() => {
    const parts = cursor === '/' ? [] : cursor.split('/').filter(Boolean)
    const out: { label: string; path: string }[] = [{ label: t('meta:storage.root'), path: '/' }]
    let p = ''
    for (const part of parts) {
      p += `/${part}`
      out.push({ label: part, path: p })
    }
    return out
  }, [cursor, t])
  const openFolder = (path: string) =>
    void navigate({ to: '/files/$', params: { _splat: pathToSplat(path) } })
  const openLocation = (path: string) => {
    const i = path.lastIndexOf('/')
    const dir = i <= 0 ? '/' : path.slice(0, i)
    void navigate({
      to: '/files/$',
      params: { _splat: pathToSplat(dir) },
      search: { select: path.slice(i + 1) },
    })
  }
  const height = Math.max(240, Math.min(480, Math.round(width * 0.5)))

  return (
    <div data-testid="storage-summary-tab">
      <div className="flex flex-wrap items-center gap-2 px-4 pt-3">
        <div className="ml-auto flex items-center gap-2">
          {data ? (
            <span className="text-xs text-text-muted" data-testid="storage-generated">
              {t('meta:storage.generatedAt', { at: formatRelativeTime(data.generatedAt, lang) })}
              {data.cached ? ` ${t('meta:storage.cached')}` : ''}
            </span>
          ) : null}
          <Button
            size="sm"
            variant="outline"
            onClick={() => recompute.mutate()}
            disabled={recompute.isPending}
            data-testid="storage-recompute"
          >
            <RotateCw className={cn(recompute.isPending && 'animate-spin')} aria-hidden="true" />
            {recompute.isPending ? t('meta:storage.computing') : t('meta:storage.recompute')}
          </Button>
        </div>
      </div>
      {query.isPending ? (
        <div className="space-y-3 p-4" aria-busy="true">
          <Skeleton className="h-64 w-full" />
          <Skeleton className="h-6 w-1/2" />
        </div>
      ) : query.isError || !data ? (
        <EmptyState
          icon={PieChart}
          title={t('files:error.title')}
          description={query.error instanceof Error ? query.error.message : ''}
          className="h-full"
          action={
            <Button variant="outline" onClick={() => void query.refetch()}>
              {t('files:error.retry')}
            </Button>
          }
        />
      ) : (
        <div className="space-y-6 p-4">
          {data.partial ? (
            <p
              className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
              role="status"
            >
              {t('meta:storage.partial')}
            </p>
          ) : null}
          <section
            className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"
            data-testid="storage-summary"
          >
            <Stat
              label={t('meta:storage.total')}
              value={formatBytes(data.total, lang)}
              testid="storage-total"
              data={data.total}
              hint={`${t('meta:storage.files', { count: data.files, n: formatNumber(data.files, lang) })} · ${t('meta:storage.dirs', { count: data.dirs, n: formatNumber(data.dirs, lang) })}`}
            />
            <Stat label={t('meta:storage.trash')} value={formatBytes(data.trash ?? 0, lang)} />
            <Stat
              label={t('meta:storage.versions')}
              value={formatBytes(data.versions ?? 0, lang)}
            />
            <Stat
              label={t('meta:storage.quota')}
              value={
                data.quota && data.quota.limit > 0
                  ? `${formatBytes(data.quota.used + (data.trash ?? 0) + (data.versions ?? 0), lang)} / ${formatBytes(data.quota.limit, lang)}`
                  : formatBytes(data.quota?.used ?? data.total, lang)
              }
            />
          </section>

          <section className="space-y-2">
            <div className="flex flex-wrap items-center gap-1">
              <h2 className="text-sm font-semibold">{t('meta:storage.treemap')}</h2>
              <nav
                aria-label={t('meta:storage.treemap')}
                className="ml-2 flex items-center gap-0.5 text-xs"
              >
                {crumbs.map((c, i) => (
                  <span key={c.path} className="flex items-center gap-0.5">
                    {i > 0 ? (
                      <ChevronRight className="size-3 text-text-muted" aria-hidden="true" />
                    ) : null}
                    <button
                      type="button"
                      className={cn(
                        'rounded px-1 hover:bg-surface-2',
                        i === crumbs.length - 1 && 'font-medium',
                      )}
                      onClick={() => setCursor(c.path)}
                      aria-current={i === crumbs.length - 1 ? 'location' : undefined}
                      data-testid="storage-crumb"
                    >
                      {c.label}
                    </button>
                  </span>
                ))}
              </nav>
              {cursor !== '/' ? (
                <Button
                  size="sm"
                  variant="ghost"
                  className="ml-auto h-7"
                  onClick={() => openFolder(cursor)}
                  data-testid="storage-open-folder"
                >
                  <FolderOpen aria-hidden="true" />
                  {t('files:actions.open')}
                </Button>
              ) : null}
            </div>
            <p className="text-xs text-text-muted">{t('meta:storage.treemapHint')}</p>
            <div ref={ref} className="w-full">
              {node && node.size > 0 && width > 0 ? (
                <Treemap
                  node={node}
                  width={width}
                  height={height}
                  onOpen={(n) => {
                    if (n.children && n.children.length > 0) setCursor(n.path)
                    else openFolder(n.path)
                  }}
                />
              ) : (
                <EmptyState icon={PieChart} title={t('meta:storage.empty')} className="h-48" />
              )}
            </div>
          </section>

          <section className="grid gap-6 lg:grid-cols-[1fr_280px]">
            <div className="space-y-2">
              <h2 className="text-sm font-semibold">{t('meta:storage.topFiles')}</h2>
              <div
                className="overflow-hidden rounded-md border border-border"
                role="table"
                data-testid="storage-top"
              >
                <div
                  className="flex items-center gap-2 border-b border-border bg-surface-2 px-3 py-1.5 text-xs font-medium text-text-muted"
                  role="row"
                >
                  <span className="min-w-0 flex-1" role="columnheader">
                    {t('meta:storage.columns.name')}
                  </span>
                  <span className="hidden w-56 md:block" role="columnheader">
                    {t('meta:storage.columns.location')}
                  </span>
                  <span className="w-20 text-right" role="columnheader">
                    {t('meta:storage.columns.size')}
                  </span>
                  <span className="hidden w-28 lg:block" role="columnheader">
                    {t('meta:storage.columns.mtime')}
                  </span>
                  <span className="w-16" />
                </div>
                {data.topFiles.length === 0 ? (
                  <p className="px-3 py-4 text-xs text-text-muted">{t('meta:storage.empty')}</p>
                ) : (
                  data.topFiles.map((f) => {
                    const i = f.path.lastIndexOf('/')
                    const dir = i <= 0 ? '/' : f.path.slice(0, i)
                    return (
                      <div
                        key={f.path}
                        className="flex items-center gap-2 border-b border-border/60 px-3 py-1 text-sm last:border-b-0 hover:bg-surface-2"
                        role="row"
                        data-testid="storage-top-row"
                        data-path={f.path}
                        data-size={f.size}
                      >
                        <span className="flex min-w-0 flex-1 items-center gap-2" role="cell">
                          <FileIcon category={toIconCategory(f.category)} size={16} />
                          <span className="truncate" title={f.name}>
                            {f.name}
                          </span>
                        </span>
                        <span
                          className="hidden w-56 truncate font-mono text-xs text-text-muted md:block"
                          role="cell"
                          title={dir}
                        >
                          {dir}
                        </span>
                        <span className="w-20 text-right tabular" role="cell">
                          {formatBytes(f.size, lang)}
                        </span>
                        <span
                          className="hidden w-28 text-xs text-text-muted lg:block"
                          role="cell"
                          title={formatDateTimeFull(f.mtime, lang)}
                        >
                          {formatRelativeTime(f.mtime, lang)}
                        </span>
                        <span className="flex w-16 justify-end gap-0.5" role="cell">
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label={t('meta:storage.openLocation')}
                            onClick={() => openLocation(f.path)}
                            data-testid="storage-top-open"
                          >
                            <FolderOpen aria-hidden="true" />
                          </Button>
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            className="text-danger"
                            aria-label={t('meta:storage.deleteFile')}
                            onClick={() =>
                              void ops.remove([f.path]).then((res) => {
                                if (res) void qc.invalidateQueries({ queryKey: STORAGE_QUERY_KEY })
                              })
                            }
                            data-testid="storage-top-delete"
                          >
                            <Trash2 aria-hidden="true" />
                          </Button>
                        </span>
                      </div>
                    )
                  })
                )}
              </div>
            </div>
            <div className="space-y-2">
              <h2 className="text-sm font-semibold">{t('meta:storage.byType')}</h2>
              <dl
                className="space-y-2 rounded-md border border-border p-3 text-sm"
                data-testid="storage-by-type"
              >
                {TYPE_CLASSES.map((c) => {
                  const v = data.byType[c] ?? { bytes: 0, files: 0 }
                  const pct = data.total > 0 ? Math.round((v.bytes / data.total) * 100) : 0
                  return (
                    // dl > div > dt/dd only (axe definition-list/dlitem, review B M-05):
                    // the bar is a second, presentational dd spanning both columns
                    <div
                      key={c}
                      className="grid grid-cols-[1fr_auto] items-center gap-x-2 gap-y-0.5"
                    >
                      <dt className="flex items-center gap-2">
                        <FileIcon
                          category={toIconCategory(c === 'document' ? 'document' : c)}
                          size={16}
                        />
                        {t(`meta:storage.types.${c}`)}
                      </dt>
                      <dd className="tabular text-text-muted">
                        {formatBytes(v.bytes, lang)} · {formatNumber(v.files, lang)}
                      </dd>
                      <dd
                        className="col-span-2 h-1 overflow-hidden rounded-full bg-surface-2"
                        aria-hidden="true"
                      >
                        <div
                          className="h-full rounded-full bg-accent"
                          style={{ width: `${pct}%` }}
                        />
                      </dd>
                    </div>
                  )
                })}
              </dl>
            </div>
          </section>
        </div>
      )}
    </div>
  )
}

function Stat({
  label,
  value,
  hint,
  testid,
  data,
}: {
  label: string
  value: string
  hint?: string
  testid?: string
  data?: number
}) {
  return (
    <div
      className="rounded-md border border-border bg-surface p-3"
      data-testid={testid}
      data-value={data}
    >
      <div className="text-xs text-text-muted">{label}</div>
      <div className="text-lg font-semibold tabular">{value}</div>
      {hint ? <div className="text-xs text-text-muted">{hint}</div> : null}
    </div>
  )
}
