import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { Download, Eye, GitCompare, RotateCcw, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { formatBytes, formatDateTimeFull, formatRelativeTime } from '@/lib/format'
import { errorMessage } from '@/lib/errors'
import { startDownload } from '@/lib/media-url'
import { useWorkspace } from './controller'
import { useVersionActions } from './useVersionActions'
import {
  listVersions,
  versionDownloadUrl,
  versionsQueryKey,
  type FileVersion,
  type VersionsResponse,
} from './versionsApi'

export interface VersionListProps {
  path: string
  data: VersionsResponse | undefined
  loading?: boolean
  error?: unknown
  onOpen?: ((v: FileVersion) => void) | undefined
  onCompare?: ((v: FileVersion) => void) | undefined
  onRestore: (v: FileVersion) => void
  onDelete: (v: FileVersion) => void
  onDownload?: ((v: FileVersion) => void) | undefined
}

/** Presentational list of versions (SPEC §8.13 "Riwayat versi"): relative +
 *  absolute time, size, reason, actions. Shared by the editor dialog and the
 *  Properties → Riwayat versi tab. */
export function VersionList(p: VersionListProps) {
  const { t, i18n } = useTranslation('editor')
  const lang = i18n.resolvedLanguage ?? 'id'
  if (p.loading) return <p className="py-4 text-sm text-text-muted">…</p>
  if (p.error)
    return (
      <p className="py-4 text-sm text-destructive" role="alert">
        {t('versions.loadError', { message: errorMessage(p.error) })}
      </p>
    )
  if (p.data && !p.data.enabled)
    return (
      <p className="py-4 text-sm text-text-muted" data-testid="versions-disabled">
        {t('versions.disabled')}
      </p>
    )
  const items = p.data?.items ?? []
  if (items.length === 0)
    return (
      <p className="py-4 text-sm text-text-muted" data-testid="versions-empty">
        {t('versions.empty')}
      </p>
    )
  return (
    <div className="flex min-h-0 flex-col gap-2">
      <p className="text-xs text-text-muted">
        {t('versions.count', { count: items.length })} · {t('versions.limit', { n: p.data?.limit })}
      </p>
      <ul
        className="min-h-0 divide-y divide-border overflow-auto rounded-md border border-border"
        data-testid="version-list"
      >
        {items.map((v) => (
          <li
            key={v.id}
            className="flex flex-wrap items-center gap-2 px-3 py-2 text-sm"
            data-testid="version-row"
            data-version-no={v.no}
          >
            <span className="w-10 shrink-0 font-mono text-xs text-text-muted">
              {t('versions.no', { no: v.no })}
            </span>
            <span className="min-w-0 flex-1">
              <span title={formatDateTimeFull(v.createdAt, lang)}>
                {formatRelativeTime(v.createdAt, lang)}
              </span>
              <span className="ml-2 text-xs text-text-muted">
                {formatDateTimeFull(v.createdAt, lang)} · {formatBytes(v.size, lang)} ·{' '}
                {t(`versions.reason.${v.reason}`, { defaultValue: v.reason })}
              </span>
            </span>
            <span className="flex items-center gap-0.5">
              {p.onOpen ? (
                <IconButton
                  size="icon-xs"
                  label={t('versions.open')}
                  onClick={() => p.onOpen?.(v)}
                  data-testid="version-open"
                >
                  <Eye />
                </IconButton>
              ) : null}
              {p.onCompare ? (
                <IconButton
                  size="icon-xs"
                  label={t('versions.compare')}
                  onClick={() => p.onCompare?.(v)}
                  data-testid="version-compare"
                >
                  <GitCompare />
                </IconButton>
              ) : null}
              <IconButton
                size="icon-xs"
                label={t('versions.restore')}
                onClick={() => p.onRestore(v)}
                data-testid="version-restore"
              >
                <RotateCcw />
              </IconButton>
              <IconButton
                size="icon-xs"
                label={t('versions.download')}
                onClick={() =>
                  p.onDownload ? p.onDownload(v) : startDownload(versionDownloadUrl(v.id))
                }
                data-testid="version-download"
              >
                <Download />
              </IconButton>
              <IconButton
                size="icon-xs"
                label={t('versions.delete')}
                onClick={() => p.onDelete(v)}
                data-testid="version-delete"
              >
                <Trash2 />
              </IconButton>
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Version history dialog of the editor (status bar "Riwayat (n)"). */
export function VersionHistoryDialog({
  path,
  onClose,
}: {
  path: string | null
  onClose: () => void
}) {
  const { t } = useTranslation('editor')
  const ws = useWorkspace()
  const q = useQuery({
    queryKey: versionsQueryKey(path ?? ''),
    queryFn: ({ signal }) => listVersions(path!, signal),
    enabled: !!path,
  })
  const actions = useVersionActions(path ?? '', {
    onRestore: async (v) => {
      const ok = await ws.restoreVersion(path!, v)
      return ok
    },
  })
  const name = path?.split('/').pop() ?? ''
  return (
    <>
      <Dialog open={!!path} onOpenChange={(o) => !o && onClose()}>
        <DialogContent
          className="flex max-h-[85vh] flex-col sm:max-w-2xl"
          data-testid="version-history"
        >
          <DialogHeader>
            <DialogTitle>{t('versions.titleFor', { name })}</DialogTitle>
            <DialogDescription className="sr-only">{t('versions.title')}</DialogDescription>
          </DialogHeader>
          {path ? (
            <VersionList
              path={path}
              data={q.data}
              loading={q.isPending}
              error={q.error}
              onOpen={(v) => {
                ws.openVersion(path, v)
                onClose()
              }}
              onCompare={(v) => void ws.compareVersion(path, v)}
              onRestore={(v) => actions.confirm({ kind: 'restore', v })}
              onDelete={(v) => actions.confirm({ kind: 'delete', v })}
            />
          ) : null}
          <DialogFooter>
            {q.data?.items.length ? (
              <Button
                variant="outline"
                onClick={() =>
                  actions.confirm({
                    kind: 'deleteAll',
                    v: { no: q.data?.items.length ?? 0 } as FileVersion,
                  })
                }
                data-testid="versions-delete-all"
              >
                {t('versions.deleteAll')}
              </Button>
            ) : null}
            <Button variant="outline" onClick={onClose}>
              {t('dialog.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {actions.dialog}
    </>
  )
}
