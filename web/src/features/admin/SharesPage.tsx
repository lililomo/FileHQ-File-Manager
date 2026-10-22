import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Ban, Link2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { errorMessage } from '@/lib/errors'
import { formatDate, formatNumber } from '@/lib/format'
import { useDebouncedValue } from '@/lib/useDebouncedValue'
import { ConfirmActionDialog } from './UserDialogs'
import { listAdminShares, opsKeys, revokeAdminShare, type AdminShare } from './opsApi'

const STATUS_VARIANT = {
  active: 'outline',
  disabled: 'destructive',
  expired: 'secondary',
  broken: 'destructive',
} as const

/** `/admin/shares` (SPEC §8.17, §6.3 GET /admin/shares): every share as
 *  metadata only (item name, owner, status, expiry, counters — never the
 *  link or the content) with a "disable" action (admin_revoked, audited). */
export function SharesPage() {
  const { t, i18n } = useTranslation(['admin', 'common'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const [mode, setMode] = useState('public')
  const [status, setStatus] = useState('')
  const [owner, setOwner] = useState('')
  const debouncedOwner = useDebouncedValue(owner, 300)
  const [pending, setPending] = useState<AdminShare | null>(null)
  const query = useInfiniteQuery({
    queryKey: [...opsKeys.shares, mode, status, debouncedOwner],
    queryFn: ({ pageParam, signal }) =>
      listAdminShares({ mode, status, owner: debouncedOwner }, pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  })
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data])
  const revoke = useMutation({
    mutationFn: (s: AdminShare) => revokeAdminShare(s.id),
    onSuccess: (_, s) => {
      setPending(null)
      toast.success(t('admin:shares.revoked', { name: s.name }))
      void qc.invalidateQueries({ queryKey: opsKeys.shares })
    },
    onError: (e) => {
      setPending(null)
      toast.error(errorMessage(e))
    },
  })
  const select = 'h-8 rounded-md border border-border bg-surface px-2 text-sm'
  return (
    <div className="flex h-full flex-col" data-testid="admin-shares">
      <header className="flex flex-wrap items-end gap-2 border-b border-border px-4 py-3">
        <h1 className="mr-2 self-center text-lg font-semibold">{t('admin:shares.title')}</h1>
        <div className="grid gap-1">
          <Label htmlFor="shares-mode" className="text-xs">
            {t('admin:shares.mode')}
          </Label>
          <select
            id="shares-mode"
            value={mode}
            onChange={(e) => setMode(e.target.value)}
            className={select}
          >
            <option value="public">{t('admin:shares.modePublic')}</option>
            <option value="users">{t('admin:shares.modeUsers')}</option>
            <option value="">{t('admin:shares.modeAll')}</option>
          </select>
        </div>
        <div className="grid gap-1">
          <Label htmlFor="shares-status" className="text-xs">
            {t('admin:shares.status')}
          </Label>
          <select
            id="shares-status"
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            className={select}
          >
            <option value="">{t('admin:shares.statusAll')}</option>
            {(['active', 'disabled', 'expired', 'broken'] as const).map((s) => (
              <option key={s} value={s}>
                {t(`admin:shares.st.${s}`)}
              </option>
            ))}
          </select>
        </div>
        <div className="grid gap-1">
          <Label htmlFor="shares-owner" className="text-xs">
            {t('admin:shares.owner')}
          </Label>
          <Input
            id="shares-owner"
            value={owner}
            onChange={(e) => setOwner(e.target.value)}
            className="h-8 w-40"
          />
        </div>
      </header>
      {query.isPending ? (
        <div className="space-y-2 p-3" aria-busy="true">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : query.isError ? (
        <EmptyState icon={Link2} title={errorMessage(query.error)} />
      ) : rows.length === 0 ? (
        <EmptyState icon={Link2} title={t('admin:shares.empty')} />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <Table data-testid="shares-table" className="text-xs">
            <TableHeader>
              <TableRow>
                <TableHead>{t('admin:shares.colItem')}</TableHead>
                <TableHead>{t('admin:shares.owner')}</TableHead>
                <TableHead>{t('admin:shares.status')}</TableHead>
                <TableHead>{t('admin:shares.colExpires')}</TableHead>
                <TableHead className="text-right">{t('admin:shares.colViews')}</TableHead>
                <TableHead className="text-right">{t('admin:shares.colDownloads')}</TableHead>
                <TableHead>{t('admin:shares.colCreated')}</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((s) => (
                <TableRow key={s.id} data-testid="share-row" data-status={s.status}>
                  <TableCell>
                    <div className="font-medium">{s.name}</div>
                    <div className="max-w-72 truncate text-text-muted" title={s.path}>
                      {s.path}
                    </div>
                  </TableCell>
                  <TableCell>{s.ownerUsername}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      <Badge variant={STATUS_VARIANT[s.status]}>
                        {t(`admin:shares.st.${s.status}`)}
                      </Badge>
                      {s.disabledReason === 'admin_revoked' ? (
                        <Badge variant="outline">{t('admin:shares.byAdmin')}</Badge>
                      ) : null}
                      {s.hasPassword ? (
                        <Badge variant="outline">{t('admin:shares.password')}</Badge>
                      ) : null}
                      {s.mode === 'users' ? (
                        <Badge variant="secondary">
                          {t('admin:shares.recipients', { count: s.recipientCount })}
                        </Badge>
                      ) : null}
                    </div>
                  </TableCell>
                  <TableCell>
                    {s.expiresAt
                      ? formatDate(s.expiresAt, lang, { time: true })
                      : t('admin:shares.never')}
                  </TableCell>
                  <TableCell className="text-right tabular">
                    {formatNumber(s.viewCount, lang)}
                  </TableCell>
                  <TableCell className="text-right tabular">
                    {formatNumber(s.downloadCount, lang)}
                    {s.maxDownloads ? ` / ${formatNumber(s.maxDownloads, lang)}` : ''}
                  </TableCell>
                  <TableCell>{formatDate(s.createdAt, lang)}</TableCell>
                  <TableCell className="text-right">
                    {s.status !== 'disabled' ? (
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t('admin:shares.revoke')}
                        title={t('admin:shares.revoke')}
                        onClick={() => setPending(s)}
                        data-testid="share-revoke"
                      >
                        <Ban className="size-4" aria-hidden="true" />
                      </Button>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {query.hasNextPage ? (
            <div className="p-3 text-center">
              <Button variant="outline" size="sm" onClick={() => void query.fetchNextPage()}>
                {t('admin:audit.more')}
              </Button>
            </div>
          ) : null}
        </div>
      )}
      {pending ? (
        <ConfirmActionDialog
          title={t('admin:shares.revokeTitle', { name: pending.name })}
          body={t('admin:shares.revokeBody', { owner: pending.ownerUsername })}
          destructive
          pending={revoke.isPending}
          onConfirm={() => revoke.mutate(pending)}
          onCancel={() => setPending(null)}
          testId="share-revoke-dialog"
        />
      ) : null}
    </div>
  )
}
