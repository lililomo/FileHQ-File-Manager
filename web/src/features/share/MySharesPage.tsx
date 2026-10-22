import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Copy, History, Link2, QrCode as QrIcon, Users } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { pathToSplat } from '@/features/browser/path'
import { errorMessage } from '@/lib/errors'
import { formatDate } from '@/lib/format'
import { AccessLogDialog } from './AccessLogDialog'
import { QrCode } from './QrCode'
import { ShareDialog } from './ShareDialog'
import {
  deleteShare,
  disableShare,
  enableShare,
  listShares,
  type Share,
  type ShareMode,
  type SharePage,
} from './api'

type Filter = 'all' | ShareMode

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

/** `/shares` ("Share saya", SPEC §8.15): every share of the user — item,
 *  mode, recipients, protection, expiry, counts, last access, status — with
 *  copy/QR/manage/disable/delete and the access journal. */
export function MySharesPage() {
  const { t, i18n } = useTranslation(['share', 'nav'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [filter, setFilter] = useState<Filter>('all')
  const [logFor, setLogFor] = useState<Share | null>(null)
  const [manage, setManage] = useState<Share | null>(null)
  const [qrFor, setQrFor] = useState<Share | null>(null)
  const query = useInfiniteQuery<
    SharePage,
    Error,
    { pages: SharePage[] },
    readonly unknown[],
    string | undefined
  >({
    queryKey: ['shares', 'mine', filter],
    queryFn: ({ pageParam }) =>
      listShares({
        ...(pageParam ? { cursor: pageParam } : {}),
        ...(filter === 'all' ? {} : { mode: filter }),
      }),
    initialPageParam: undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  })
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data])
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ['shares'] })
    void qc.invalidateQueries({ queryKey: ['list'] })
  }
  const run = useMutation({
    mutationFn: async ({
      share,
      action,
    }: {
      share: Share
      action: 'disable' | 'enable' | 'delete'
    }) => {
      if (action === 'disable') await disableShare(share.id)
      else if (action === 'enable') await enableShare(share.id)
      else await deleteShare(share.id)
      return action
    },
    onSuccess: (action) => {
      toast.success(
        t(
          `share:toast.${action === 'delete' ? 'deleted' : action === 'disable' ? 'disabled' : 'enabled'}`,
        ),
      )
      invalidate()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })

  return (
    <div className="flex h-full flex-col" data-testid="my-shares-page">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold">{t('nav:myShares')}</h1>
        <span className="flex-1" />
        <Select value={filter} onValueChange={(v) => setFilter(v as Filter)}>
          <SelectTrigger
            className="h-8 w-40"
            aria-label={t('share:mine.mode')}
            data-testid="shares-filter"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('share:mine.filterAll')}</SelectItem>
            <SelectItem value="public">{t('share:mine.filterPublic')}</SelectItem>
            <SelectItem value="users">{t('share:mine.filterUsers')}</SelectItem>
          </SelectContent>
        </Select>
      </header>
      {query.isPending ? (
        <div className="space-y-2 p-3" aria-busy="true">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : query.isError ? (
        <EmptyState
          icon={Link2}
          title={t('share:public.error')}
          description={errorMessage(query.error)}
          className="h-full"
        />
      ) : rows.length === 0 ? (
        <EmptyState
          icon={Link2}
          title={t('share:mine.empty')}
          description={t('share:mine.emptyHint')}
          className="h-full"
        />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('share:mine.item')}</TableHead>
                <TableHead>{t('share:mine.mode')}</TableHead>
                <TableHead>{t('share:mine.recipients')}</TableHead>
                <TableHead className="hidden md:table-cell">{t('share:mine.protection')}</TableHead>
                <TableHead className="hidden md:table-cell">{t('share:mine.expiry')}</TableHead>
                <TableHead className="hidden lg:table-cell">{t('share:mine.stats')}</TableHead>
                <TableHead className="hidden lg:table-cell">{t('share:mine.lastAccess')}</TableHead>
                <TableHead>{t('share:mine.status')}</TableHead>
                <TableHead className="text-right">{t('share:mine.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((s) => {
                const reason = s.disabledReason ? t(`share:row.reason.${s.disabledReason}`) : null
                return (
                  <TableRow
                    key={s.id}
                    data-testid="my-share-row"
                    data-mode={s.mode}
                    data-status={s.status}
                  >
                    <TableCell className="max-w-56">
                      <button
                        type="button"
                        className="flex min-w-0 items-center gap-2 text-left hover:underline"
                        title={t('share:mine.openLocation')}
                        onClick={() =>
                          void navigate({
                            to: '/files/$',
                            params: {
                              _splat: pathToSplat(s.path.slice(0, s.path.lastIndexOf('/')) || '/'),
                            },
                            search: { select: s.name },
                          })
                        }
                      >
                        <FileIcon
                          category={s.isDir ? 'folder' : 'other'}
                          size={18}
                          className="shrink-0"
                        />
                        <span className="truncate">{s.name}</span>
                      </button>
                      <p className="truncate text-xs text-text-muted" title={s.path}>
                        {s.path}
                      </p>
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline">
                        {s.mode === 'public' ? (
                          <Link2 className="size-3" aria-hidden="true" />
                        ) : (
                          <Users className="size-3" aria-hidden="true" />
                        )}
                        {s.mode === 'public'
                          ? t('share:mine.modePublic')
                          : t('share:mine.modeUsers')}
                      </Badge>
                    </TableCell>
                    <TableCell className="max-w-48">
                      {s.mode === 'users' ? (
                        <span className="flex flex-wrap gap-1" data-testid="my-share-recipients">
                          {(s.recipients ?? []).map((r) => (
                            <Badge
                              key={r.username}
                              variant="secondary"
                              title={r.inactive ? t('share:users.inactive') : undefined}
                            >
                              {r.username} · {t(`share:users.${r.permission}`)}
                            </Badge>
                          ))}
                        </span>
                      ) : (
                        <span className="text-xs text-text-muted">{t('share:mine.never')}</span>
                      )}
                    </TableCell>
                    <TableCell className="hidden text-xs md:table-cell">
                      {s.hasPassword ? (
                        <Badge variant="secondary">{t('share:mine.password')}</Badge>
                      ) : null}{' '}
                      {s.maxDownloads ? (
                        <Badge variant="secondary">
                          {t('share:mine.limit', { n: s.maxDownloads })}
                        </Badge>
                      ) : null}
                      {!s.allowDownload ? (
                        <Badge variant="secondary">{t('share:result.previewOnly')}</Badge>
                      ) : null}
                    </TableCell>
                    <TableCell className="hidden text-xs md:table-cell">
                      {s.expiresAt
                        ? formatDate(s.expiresAt, lang, { time: true })
                        : t('share:mine.never')}
                    </TableCell>
                    <TableCell className="hidden text-xs lg:table-cell">
                      {s.viewCount} / {s.downloadCount}
                    </TableCell>
                    <TableCell className="hidden text-xs lg:table-cell">
                      {s.lastAccessAt
                        ? formatDate(s.lastAccessAt, lang, { time: true })
                        : t('share:mine.never')}
                    </TableCell>
                    <TableCell>
                      <Badge variant={s.status === 'active' ? 'default' : 'secondary'}>
                        {t(`share:row.status.${s.status}`)}
                        {reason ? ` · ${reason}` : ''}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right whitespace-nowrap">
                      {s.mode === 'public' && s.url ? (
                        <>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={t('share:mine.copy')}
                            onClick={async () => {
                              if (await copyText(s.url)) toast.success(t('share:result.copied'))
                              else toast.error(t('share:toast.copyFailed'))
                            }}
                            data-testid="my-share-copy"
                          >
                            <Copy aria-hidden="true" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={t('share:mine.qr')}
                            onClick={() => setQrFor(s)}
                          >
                            <QrIcon aria-hidden="true" />
                          </Button>
                        </>
                      ) : null}
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t('share:mine.accessLog')}
                        onClick={() => setLogFor(s)}
                        data-testid="my-share-log"
                      >
                        <History aria-hidden="true" />
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setManage(s)}
                        data-testid="my-share-manage"
                      >
                        {t('share:mine.edit')}
                      </Button>
                      {s.status === 'disabled' ? (
                        s.disabledReason === 'manual' || s.disabledReason === 'item_trashed' ? (
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => run.mutate({ share: s, action: 'enable' })}
                          >
                            {t('share:row.enable')}
                          </Button>
                        ) : null
                      ) : (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => run.mutate({ share: s, action: 'disable' })}
                          data-testid="my-share-disable"
                        >
                          {t('share:row.disable')}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          {query.hasNextPage ? (
            <div className="p-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
              >
                {t('share:mine.loadMore')}
              </Button>
            </div>
          ) : null}
        </div>
      )}
      {logFor ? <AccessLogDialog share={logFor} onClose={() => setLogFor(null)} /> : null}
      {manage ? (
        <ShareDialog
          item={{ path: manage.path, name: manage.name, isDir: manage.isDir }}
          onClose={() => {
            setManage(null)
            invalidate()
          }}
        />
      ) : null}
      <Dialog open={qrFor !== null} onOpenChange={(open) => !open && setQrFor(null)}>
        <DialogContent className="sm:max-w-xs">
          <DialogHeader>
            <DialogTitle>{t('share:result.qrLabel')}</DialogTitle>
          </DialogHeader>
          {qrFor?.url ? (
            <div className="flex justify-center">
              <QrCode value={qrFor.url} size={200} />
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  )
}
