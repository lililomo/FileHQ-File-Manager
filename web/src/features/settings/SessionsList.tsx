import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { LogOut, MonitorSmartphone } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ConfirmActionDialog } from '@/features/admin/UserDialogs'
import { errorMessage } from '@/lib/errors'
import { formatDate, formatRelativeTime } from '@/lib/format'
import {
  deleteSession,
  listSessions,
  revokeOtherSessions,
  sessionsKey,
  type SessionView,
} from './api'

/** Active sessions (SPEC §8.18): device, IP, created, last active, "this
 *  session" badge, logout per session and "log out every other session". */
export function SessionsList() {
  const { t, i18n } = useTranslation('auth')
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const [confirm, setConfirm] = useState<SessionView | 'others' | null>(null)
  const query = useQuery({ queryKey: sessionsKey, queryFn: ({ signal }) => listSessions(signal) })
  const done = (msg: string) => {
    setConfirm(null)
    toast.success(msg)
    void qc.invalidateQueries({ queryKey: sessionsKey })
  }
  const one = useMutation({
    mutationFn: (s: SessionView) => deleteSession(s.id),
    onSuccess: (_, s) => done(t('profile.sessions.loggedOut', { device: s.deviceLabel })),
    onError: (e) => {
      setConfirm(null)
      toast.error(errorMessage(e))
    },
  })
  const others = useMutation({
    mutationFn: revokeOtherSessions,
    onSuccess: () => done(t('profile.sessions.othersLoggedOut')),
    onError: (e) => {
      setConfirm(null)
      toast.error(errorMessage(e))
    },
  })
  const items = query.data?.items ?? []
  return (
    <section aria-labelledby="sessions-title" className="space-y-2" data-testid="sessions">
      <div className="flex flex-wrap items-center gap-2">
        <h2 id="sessions-title" className="text-base font-semibold">
          {t('profile.sessions.title')}
        </h2>
        <span className="flex-1" />
        <Button
          size="sm"
          variant="outline"
          disabled={items.length < 2 || others.isPending}
          onClick={() => setConfirm('others')}
          data-testid="sessions-revoke-others"
        >
          <LogOut className="size-4" aria-hidden="true" />
          {t('profile.sessions.logoutOthers')}
        </Button>
      </div>
      {query.isPending ? (
        <Skeleton className="h-24 w-full" />
      ) : query.isError ? (
        <p className="text-sm text-danger">{errorMessage(query.error)}</p>
      ) : (
        <Table className="text-sm" data-testid="sessions-table">
          <TableHeader>
            <TableRow>
              <TableHead>{t('profile.sessions.device')}</TableHead>
              <TableHead>{t('profile.sessions.ip')}</TableHead>
              <TableHead>{t('profile.sessions.created')}</TableHead>
              <TableHead>{t('profile.sessions.lastSeen')}</TableHead>
              <TableHead className="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((s) => (
              <TableRow key={s.id} data-testid="session-row" data-current={s.current}>
                <TableCell>
                  <span className="inline-flex items-center gap-2">
                    <MonitorSmartphone className="size-4 text-text-muted" aria-hidden="true" />
                    {s.deviceLabel}
                    {s.current ? (
                      <Badge variant="secondary" data-testid="session-current">
                        {t('profile.sessions.current')}
                      </Badge>
                    ) : null}
                  </span>
                </TableCell>
                <TableCell className="font-mono text-xs">{s.ip}</TableCell>
                <TableCell className="text-xs">
                  {formatDate(s.createdAt, lang, { time: true })}
                </TableCell>
                <TableCell
                  className="text-xs"
                  title={formatDate(s.lastSeenAt, lang, { time: true })}
                >
                  {formatRelativeTime(s.lastSeenAt, lang)}
                </TableCell>
                <TableCell className="text-right">
                  {!s.current ? (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t('profile.sessions.logout', { device: s.deviceLabel })}
                      title={t('profile.sessions.logout', { device: s.deviceLabel })}
                      onClick={() => setConfirm(s)}
                      data-testid="session-logout"
                    >
                      <LogOut className="size-4" aria-hidden="true" />
                    </Button>
                  ) : null}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {confirm ? (
        <ConfirmActionDialog
          title={
            confirm === 'others'
              ? t('profile.sessions.logoutOthers')
              : t('profile.sessions.logout', { device: confirm.deviceLabel })
          }
          body={t(
            confirm === 'others' ? 'profile.sessions.confirmOthers' : 'profile.sessions.confirmOne',
          )}
          destructive
          pending={one.isPending || others.isPending}
          onCancel={() => setConfirm(null)}
          onConfirm={() => (confirm === 'others' ? others.mutate() : one.mutate(confirm))}
          testId="session-confirm"
        />
      ) : null}
    </section>
  )
}
