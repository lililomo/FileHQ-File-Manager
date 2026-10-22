import { useInfiniteQuery } from '@tanstack/react-query'
import { Download, ScrollText } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/EmptyState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useDebouncedValue } from '@/lib/useDebouncedValue'
import { errorMessage } from '@/lib/errors'
import { formatDateTimeFull } from '@/lib/format'
import { auditExportUrl, listAudit, opsKeys, type AuditEntry, type AuditFilter } from './opsApi'

/** Action groups offered in the filter (prefix match on the server). */
const ACTION_GROUPS = [
  'auth.',
  'user.',
  'settings.',
  'share.',
  'file.',
  'trash.',
  'admin.',
  'maintenance.',
  'quota.',
  'system.',
]

/** `/admin/audit` (SPEC §8.16 "Audit (admin)"): dense newest-first table with
 *  actor/action/date/text filters, details as JSON in a side sheet and the
 *  streaming CSV export of the same filter. */
export function AuditPage() {
  const { t, i18n } = useTranslation(['admin', 'common'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const [filter, setFilter] = useState<AuditFilter>({})
  const debounced = useDebouncedValue(filter, 300)
  const [open, setOpen] = useState<AuditEntry | null>(null)
  const query = useInfiniteQuery({
    queryKey: [...opsKeys.audit, debounced],
    queryFn: ({ pageParam, signal }) => listAudit(debounced, pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  })
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data])
  const set =
    (k: keyof AuditFilter) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setFilter((f) => ({ ...f, [k]: e.target.value }))

  return (
    <div className="flex h-full flex-col" data-testid="admin-audit">
      <header className="flex flex-wrap items-end gap-2 border-b border-border px-4 py-3">
        <h1 className="mr-2 self-center text-lg font-semibold">{t('admin:audit.title')}</h1>
        <div className="grid gap-1">
          <Label htmlFor="audit-actor" className="text-xs">
            {t('admin:audit.actor')}
          </Label>
          <Input
            id="audit-actor"
            value={filter.actor ?? ''}
            onChange={set('actor')}
            className="h-8 w-32"
            data-testid="audit-actor"
          />
        </div>
        <div className="grid gap-1">
          <Label htmlFor="audit-action" className="text-xs">
            {t('admin:audit.action')}
          </Label>
          <select
            id="audit-action"
            value={filter.action ?? ''}
            onChange={set('action')}
            className="h-8 rounded-md border border-border bg-surface px-2 text-sm"
            data-testid="audit-action"
          >
            <option value="">{t('admin:audit.allActions')}</option>
            {ACTION_GROUPS.map((g) => (
              <option key={g} value={g}>
                {g}*
              </option>
            ))}
          </select>
        </div>
        <div className="grid gap-1">
          <Label htmlFor="audit-from" className="text-xs">
            {t('admin:audit.from')}
          </Label>
          <Input
            id="audit-from"
            type="date"
            value={filter.from ?? ''}
            onChange={set('from')}
            className="h-8 w-36"
          />
        </div>
        <div className="grid gap-1">
          <Label htmlFor="audit-to" className="text-xs">
            {t('admin:audit.to')}
          </Label>
          <Input
            id="audit-to"
            type="date"
            value={filter.to ?? ''}
            onChange={set('to')}
            className="h-8 w-36"
          />
        </div>
        <div className="grid gap-1">
          <Label htmlFor="audit-q" className="text-xs">
            {t('admin:audit.search')}
          </Label>
          <Input
            id="audit-q"
            type="search"
            value={filter.q ?? ''}
            onChange={set('q')}
            className="h-8 w-48"
            data-testid="audit-search"
          />
        </div>
        <span className="flex-1" />
        <Button asChild size="sm" variant="outline">
          <a href={auditExportUrl(debounced)} download data-testid="audit-export">
            <Download className="size-4" aria-hidden="true" />
            {t('admin:audit.export')}
          </a>
        </Button>
      </header>
      {query.isPending ? (
        <div className="space-y-2 p-3" aria-busy="true">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      ) : query.isError ? (
        <EmptyState icon={ScrollText} title={errorMessage(query.error)} />
      ) : rows.length === 0 ? (
        <EmptyState icon={ScrollText} title={t('admin:audit.empty')} />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <Table data-testid="audit-table" className="text-xs">
            <TableHeader>
              <TableRow>
                <TableHead className="w-44">{t('admin:audit.colTime')}</TableHead>
                <TableHead>{t('admin:audit.actor')}</TableHead>
                <TableHead>{t('admin:audit.action')}</TableHead>
                <TableHead>{t('admin:audit.colTarget')}</TableHead>
                <TableHead>{t('admin:audit.colIp')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r) => (
                <TableRow
                  key={r.id}
                  data-testid="audit-row"
                  data-action={r.action}
                  className="cursor-pointer"
                  tabIndex={0}
                  onClick={() => setOpen(r)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      setOpen(r)
                    }
                  }}
                >
                  <TableCell className="tabular whitespace-nowrap">
                    {formatDateTimeFull(r.at, lang)}
                  </TableCell>
                  <TableCell>{r.actorUsername ?? t('admin:audit.system')}</TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        /failed|locked|revoked|delete|purge/.test(r.action)
                          ? 'destructive'
                          : 'secondary'
                      }
                      className="font-mono"
                    >
                      {r.action}
                    </Badge>
                  </TableCell>
                  <TableCell className="max-w-64 truncate font-mono" title={r.targetId ?? ''}>
                    {r.targetType ? `${r.targetType}:${r.targetId ?? ''}` : '—'}
                  </TableCell>
                  <TableCell className="font-mono">{r.ip}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {query.hasNextPage ? (
            <div className="p-3 text-center">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
              >
                {t('admin:audit.more')}
              </Button>
            </div>
          ) : null}
        </div>
      )}
      <Sheet open={open !== null} onOpenChange={(v) => !v && setOpen(null)}>
        <SheetContent className="w-full sm:max-w-lg" data-testid="audit-detail">
          <SheetHeader>
            <SheetTitle className="font-mono">{open?.action}</SheetTitle>
            <SheetDescription>{open ? formatDateTimeFull(open.at, lang) : ''}</SheetDescription>
          </SheetHeader>
          {open ? (
            <dl className="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1 px-4 text-xs">
              <dt className="text-text-muted">{t('admin:audit.actor')}</dt>
              <dd>{open.actorUsername ?? t('admin:audit.system')}</dd>
              <dt className="text-text-muted">{t('admin:audit.colTarget')}</dt>
              <dd className="font-mono break-all">
                {open.targetType ? `${open.targetType}:${open.targetId ?? ''}` : '—'}
              </dd>
              <dt className="text-text-muted">{t('admin:audit.colIp')}</dt>
              <dd className="font-mono">{open.ip}</dd>
              <dt className="text-text-muted">{t('admin:audit.userAgent')}</dt>
              <dd className="break-all">{open.userAgent}</dd>
            </dl>
          ) : null}
          <pre
            className="mx-4 mt-3 max-h-[60vh] overflow-auto rounded-md bg-surface-2 p-3 font-mono text-xs"
            data-testid="audit-detail-json"
          >
            {open?.details ? JSON.stringify(open.details, null, 2) : '{}'}
          </pre>
        </SheetContent>
      </Sheet>
    </div>
  )
}
