import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { History, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
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
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { useOpenLocation } from '@/features/browser/openLocation'
import { errorMessage } from '@/features/file-ops/useFileOps'
import { RECENT_QUERY_KEY } from '@/features/recent/api'
import { announce } from '@/lib/announce'
import { formatBytes, formatDateTimeFull, formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'
import {
  ACTIVITY_ACTIONS,
  activityQueryKey,
  clearActivity,
  listActivity,
  type ActivityEntry,
} from './api'

function detailText(e: ActivityEntry, lang: string): string {
  const d = e.details ?? {}
  const parts: string[] = []
  if (typeof d.status === 'string' && d.status !== 'ok') parts.push(String(d.status))
  if (typeof d.size === 'number') parts.push(formatBytes(d.size, lang))
  if (typeof d.mode === 'string') parts.push(d.mode)
  if (typeof d.items === 'number') parts.push(`${formatNumber(d.items, lang)} item`)
  if (typeof d.bytesFreed === 'number') parts.push(formatBytes(d.bytesFreed, lang))
  if (typeof d.files === 'number' && typeof d.dirs === 'number')
    parts.push(`${formatNumber(d.files, lang)} file · ${formatNumber(d.dirs, lang)} folder`)
  if (Array.isArray(d.algos)) parts.push(d.algos.join(', '))
  return parts.join(' · ')
}

/** `/activity` (SPEC §8.16): the user's journal with action/date/path filters,
 *  cursor paging and "Hapus riwayat saya". */
export function ActivityPage() {
  const { t, i18n } = useTranslation(['meta', 'common', 'errors', 'fileops'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const lt = t as unknown as (k: string, o?: Record<string, unknown>) => string
  const qc = useQueryClient()
  const openLocation = useOpenLocation()
  const [action, setAction] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [q, setQ] = useState('')
  const [debouncedQ, setDebouncedQ] = useState('')
  const [confirm, setConfirm] = useState(false)
  useEffect(() => {
    const h = window.setTimeout(() => setDebouncedQ(q.trim()), 250)
    return () => window.clearTimeout(h)
  }, [q])
  const filter = useMemo(
    () => ({ action, from, to, q: debouncedQ, limit: 100 }),
    [action, from, to, debouncedQ],
  )
  const query = useInfiniteQuery({
    queryKey: activityQueryKey(filter),
    queryFn: ({ pageParam, signal }) => listActivity(filter, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    staleTime: 5_000,
  })
  const items = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data])
  const retention = query.data?.pages[0]?.retentionDays ?? 0
  const clear = useMutation({
    mutationFn: clearActivity,
    onSuccess: (res) => {
      const msg = t('meta:activity.cleared', {
        count: res.deleted,
        n: formatNumber(res.deleted, lang),
      })
      toast.success(msg)
      announce(msg)
      void qc.invalidateQueries({ queryKey: ['activity'] })
      void qc.invalidateQueries({ queryKey: RECENT_QUERY_KEY })
    },
    onError: (e) => toast.error(errorMessage(lt, e)),
  })
  return (
    <div className="flex h-full flex-col" data-testid="activity-page">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold">{t('meta:activity.title')}</h1>
        <p className="w-full text-xs text-text-muted sm:ml-2 sm:w-auto">
          {t('meta:activity.subtitle', { days: retention })}
        </p>
        <Button
          size="sm"
          variant="outline"
          className="ml-auto"
          onClick={() => setConfirm(true)}
          disabled={items.length === 0}
          data-testid="activity-clear"
        >
          <Trash2 aria-hidden="true" />
          {t('meta:activity.clear')}
        </Button>
      </header>
      <div className="flex flex-wrap items-end gap-2 border-b border-border px-4 py-2">
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t('meta:activity.search')}
          aria-label={t('meta:activity.search')}
          className="h-8 w-56"
          data-testid="activity-search"
        />
        <Select value={action || 'all'} onValueChange={(v) => setAction(v === 'all' ? '' : v)}>
          <SelectTrigger
            className="h-8 w-48"
            aria-label={t('meta:activity.filterActionLabel')}
            data-testid="activity-action"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('meta:activity.filterAction')}</SelectItem>
            {ACTIVITY_ACTIONS.map((a) => (
              <SelectItem key={a} value={a}>
                {t(`meta:activity.actions.${a}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className="flex items-center gap-1">
          <Label htmlFor="activity-from" className="text-xs text-text-muted">
            {t('meta:activity.from')}
          </Label>
          <Input
            id="activity-from"
            type="date"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
            className="h-8 w-36"
            data-testid="activity-from"
          />
        </div>
        <div className="flex items-center gap-1">
          <Label htmlFor="activity-to" className="text-xs text-text-muted">
            {t('meta:activity.to')}
          </Label>
          <Input
            id="activity-to"
            type="date"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            className="h-8 w-36"
            data-testid="activity-to"
          />
        </div>
      </div>
      {query.isPending ? (
        <div className="space-y-2 p-3" aria-busy="true">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <EmptyState icon={History} title={t('meta:activity.empty')} className="h-full" />
      ) : (
        <div
          className="min-h-0 flex-1 overflow-auto"
          role="table"
          aria-label={t('meta:activity.title')}
        >
          <div
            className="sticky top-0 z-10 flex items-center gap-2 border-b border-border bg-surface-2 px-3 py-1.5 text-xs font-medium text-text-muted"
            role="row"
          >
            <span className="w-36" role="columnheader">
              {t('meta:activity.columns.at')}
            </span>
            <span className="w-32" role="columnheader">
              {t('meta:activity.columns.action')}
            </span>
            <span className="min-w-0 flex-1" role="columnheader">
              {t('meta:activity.columns.item')}
            </span>
            <span className="hidden w-56 lg:block" role="columnheader">
              {t('meta:activity.columns.target')}
            </span>
            <span className="hidden w-40 xl:block" role="columnheader">
              {t('meta:activity.columns.details')}
            </span>
          </div>
          {items.map((e) => (
            <div
              key={e.id}
              className="flex items-center gap-2 border-b border-border/60 px-3 py-1.5 text-sm hover:bg-surface-2"
              role="row"
              data-testid="activity-row"
              data-action={e.action}
            >
              <span className="w-36 text-xs tabular text-text-muted" role="cell">
                {formatDateTimeFull(e.at, lang)}
              </span>
              <span className="w-32 truncate text-xs" role="cell">
                {t(`meta:activity.actions.${e.action}`, { defaultValue: e.action })}
              </span>
              <span
                className="min-w-0 flex-1 truncate font-mono text-xs"
                role="cell"
                title={e.path ?? ''}
              >
                {e.path ? (
                  e.exists ? (
                    <button
                      type="button"
                      className="hover:underline"
                      onClick={() =>
                        openLocation({ path: e.path!, isDir: e.details?.isDir === true })
                      }
                    >
                      {e.path}
                    </button>
                  ) : (
                    <span className={cn('text-text-muted')}>
                      {e.path} {t('meta:activity.gone')}
                    </span>
                  )
                ) : (
                  '—'
                )}
              </span>
              <span
                className="hidden w-56 truncate font-mono text-xs text-text-muted lg:block"
                role="cell"
                title={e.path2 ?? ''}
              >
                {e.path2 ?? ''}
              </span>
              <span className="hidden w-40 truncate text-xs text-text-muted xl:block" role="cell">
                {detailText(e, lang)}
              </span>
            </div>
          ))}
          {query.hasNextPage ? (
            <div className="p-3 text-center">
              <Button
                size="sm"
                variant="outline"
                onClick={() => void query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
                data-testid="activity-more"
              >
                {t('meta:activity.loadMore')}
              </Button>
            </div>
          ) : null}
        </div>
      )}
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent data-testid="activity-clear-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle>{t('meta:activity.clearConfirmTitle')}</AlertDialogTitle>
            <AlertDialogDescription>{t('meta:activity.clearConfirmBody')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common:actions.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setConfirm(false)
                clear.mutate()
              }}
              data-testid="activity-clear-confirm"
            >
              {t('meta:activity.clear')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
