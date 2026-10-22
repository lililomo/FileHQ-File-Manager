import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { FileText, Folder, LogOut, Users } from 'lucide-react'
import { useState } from 'react'
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
import { Skeleton } from '@/components/ui/skeleton'
import { errorMessage } from '@/lib/errors'
import { formatDate } from '@/lib/format'
import { leaveShare, listSharedWithMe, sharedWithMeKey, type SharedItem } from './sharedApi'

/** `/shared-with-me` (SPEC §8.15): received shares grouped per owner. Active
 *  items open the recipient's browser; disabled/expired/broken items stay
 *  listed greyed out with the reason; "Keluar" removes the recipient. */
export function SharedWithMePage() {
  const { t, i18n } = useTranslation(['share', 'nav'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [leaving, setLeaving] = useState<SharedItem | null>(null)
  const query = useQuery({
    queryKey: sharedWithMeKey,
    queryFn: ({ signal }) => listSharedWithMe(signal),
    staleTime: 10_000,
  })
  const leave = useMutation({
    mutationFn: (shareId: string) => leaveShare(shareId),
    onSuccess: () => {
      toast.success(t('share:shared.left'))
      void qc.invalidateQueries({ queryKey: sharedWithMeKey })
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const groups = query.data?.groups ?? []
  const total = groups.reduce((n, g) => n + g.items.length, 0)
  const open = (it: SharedItem) =>
    void navigate({ to: '/shared-with-me/$shareId/$', params: { shareId: it.shareId, _splat: '' } })
  const statusLabel = (status: SharedItem['status']) =>
    status === 'expired'
      ? t('share:shared.status.expired')
      : status === 'broken'
        ? t('share:shared.status.broken')
        : t('share:shared.status.disabled')

  return (
    <div className="flex h-full flex-col" data-testid="shared-with-me-page">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold">{t('nav:sharedWithMe')}</h1>
        {query.data ? (
          <p className="text-xs text-text-muted sm:ml-2" data-testid="shared-count">
            {t('share:public.items', { count: total })}
          </p>
        ) : null}
      </header>
      {query.isPending ? (
        <div className="space-y-2 p-3" aria-busy="true">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : query.isError ? (
        <EmptyState
          icon={Users}
          title={t('share:public.error')}
          description={errorMessage(query.error)}
          className="h-full"
        />
      ) : total === 0 ? (
        <EmptyState
          icon={Users}
          title={t('share:shared.empty')}
          description={t('share:shared.emptyHint')}
          className="h-full"
        />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto p-3">
          {groups.map((g) => (
            <section
              key={g.owner.username}
              className="mb-4"
              aria-label={g.owner.displayName || g.owner.username}
              data-testid="shared-group"
            >
              <h2 className="mb-1 px-1 text-xs font-semibold tracking-wide text-text-muted uppercase">
                {t('share:shared.by', { name: g.owner.displayName || g.owner.username })}
              </h2>
              <ul className="divide-y divide-border rounded-lg border border-border">
                {g.items.map((it) => {
                  const active = it.status === 'active'
                  const reason = it.reason ? t(`share:shared.reason.${it.reason}`) : null
                  return (
                    <li
                      key={it.shareId}
                      className="flex items-center gap-3 px-3 py-2 text-sm"
                      data-testid="shared-item"
                      data-status={it.status}
                      data-share-id={it.shareId}
                    >
                      {it.isDir ? (
                        <Folder className="size-5 shrink-0 text-accent" aria-hidden="true" />
                      ) : (
                        <FileText className="size-5 shrink-0 text-text-muted" aria-hidden="true" />
                      )}
                      <button
                        type="button"
                        className={`min-w-0 flex-1 truncate text-left font-medium ${active ? 'hover:underline' : 'text-text-muted'}`}
                        onClick={() => active && open(it)}
                        disabled={!active}
                        data-testid="shared-open"
                      >
                        {it.name}
                      </button>
                      <Badge
                        variant={it.permission === 'editor' ? 'default' : 'secondary'}
                        data-testid="shared-perm"
                      >
                        {t(`share:users.${it.permission}`)}
                      </Badge>
                      {!active ? (
                        <Badge variant="outline" data-testid="shared-status">
                          {statusLabel(it.status)}
                          {reason ? ` · ${reason}` : ''}
                        </Badge>
                      ) : null}
                      {it.expiresAt ? (
                        <span className="hidden text-xs text-text-muted sm:inline">
                          {t('share:shared.expires', {
                            date: formatDate(it.expiresAt, lang, { time: true }),
                          })}
                        </span>
                      ) : null}
                      {it.note ? (
                        <span
                          className="hidden max-w-48 truncate text-xs text-text-muted md:inline"
                          title={it.note}
                        >
                          {it.note}
                        </span>
                      ) : null}
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t('share:shared.leave')}
                        onClick={() => setLeaving(it)}
                        data-testid="shared-leave"
                      >
                        <LogOut aria-hidden="true" />
                      </Button>
                    </li>
                  )
                })}
              </ul>
            </section>
          ))}
        </div>
      )}
      <AlertDialog open={leaving !== null} onOpenChange={(open) => !open && setLeaving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t('share:shared.leaveTitle', { name: leaving?.name ?? '' })}
            </AlertDialogTitle>
            <AlertDialogDescription>{t('share:shared.leaveBody')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('share:shared.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (leaving) leave.mutate(leaving.shareId)
                setLeaving(null)
              }}
              data-testid="shared-leave-confirm"
            >
              {t('share:shared.leave')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
