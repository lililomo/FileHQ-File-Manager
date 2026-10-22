import { useInfiniteQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { errorMessage } from '@/lib/errors'
import { formatDate } from '@/lib/format'
import { shareAccessLogs, type Share, type ShareAccessLogPage } from './api'

/** Access journal of one share (SPEC §8.15 "detail → log akses ringkas"):
 *  time, action, who (truncated IP for guests, the recipient otherwise),
 *  path inside the share. */
export function AccessLogDialog({ share, onClose }: { share: Share; onClose: () => void }) {
  const { t, i18n } = useTranslation('share')
  const lang = i18n.resolvedLanguage ?? 'id'
  const logs = useInfiniteQuery<
    ShareAccessLogPage,
    Error,
    { pages: ShareAccessLogPage[] },
    readonly unknown[],
    string | undefined
  >({
    queryKey: ['share-access-logs', share.id],
    queryFn: ({ pageParam }) => shareAccessLogs(share.id, pageParam),
    initialPageParam: undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  })
  const rows = logs.data?.pages.flatMap((p) => p.items) ?? []
  const who = new Map(share.recipients?.map((r) => [r.username, r.displayName || r.username]) ?? [])
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="max-h-[85dvh] overflow-y-auto sm:max-w-2xl"
        data-testid="access-log-dialog"
      >
        <DialogHeader>
          <DialogTitle>{t('mine.accessLogTitle', { name: share.name })}</DialogTitle>
          <DialogDescription>{share.path}</DialogDescription>
        </DialogHeader>
        {logs.isError ? (
          <p role="alert" className="text-sm text-danger">
            {errorMessage(logs.error)}
          </p>
        ) : rows.length === 0 && !logs.isPending ? (
          <p className="text-sm text-text-muted" data-testid="access-log-empty">
            {t('mine.logEmpty')}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('mine.logTime')}</TableHead>
                <TableHead>{t('mine.logAction')}</TableHead>
                <TableHead>{t('mine.logWho')}</TableHead>
                <TableHead>{t('mine.logPath')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((e) => (
                <TableRow key={e.id} data-testid="access-log-row" data-action={e.action}>
                  <TableCell className="whitespace-nowrap text-xs">
                    {formatDate(e.at, lang, { time: true })}
                  </TableCell>
                  <TableCell className="text-xs">
                    {t(`mine.action.${e.action}`, { defaultValue: e.action })}
                  </TableCell>
                  <TableCell className="text-xs">
                    {e.userId
                      ? (who.get(e.userId) ?? e.userId)
                      : t('mine.guest', { ip: e.ipTrunc })}
                  </TableCell>
                  <TableCell className="max-w-48 truncate text-xs" title={e.path ?? ''}>
                    {e.path ?? ''}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {logs.hasNextPage ? (
          <Button
            variant="outline"
            size="sm"
            onClick={() => void logs.fetchNextPage()}
            disabled={logs.isFetchingNextPage}
          >
            {t('mine.loadMore')}
          </Button>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
