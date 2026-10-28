import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { AlertTriangle, Ban, Clock, Download, Eye, FileQuestion } from 'lucide-react'
import { useCallback, useLayoutEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/EmptyState'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { PageSpinner } from '@/components/PageSpinner'
import { PublicLayout } from '@/components/shell/PublicLayout'
import { Button } from '@/components/ui/button'
import { ViewerOverlay } from '@/features/viewer/ViewerOverlay'
import { opensInViewer } from '@/features/viewer/viewers'
import { isApiError } from '@/lib/api'
import { errorMessage } from '@/lib/errors'
import { formatBytes, formatDate } from '@/lib/format'
import { setPublicScope } from '@/lib/public-scope'
import { signDownloadUrl, startDownload } from '@/lib/media-url'
import { PasswordForm } from './PasswordForm'
import { PublicBrowser } from './PublicBrowser'
import { getPublicMeta, publicMetaKey, type PublicMeta } from './publicApi'

export interface PublicPageProps {
  token: string
  /** folder path inside the share (route splat) */
  path: string
  /** file open in the viewer (`?view=`, relative to the share) */
  viewPath?: string | undefined
}

/** `/s/<token>[/sub/path]` (SPEC §8.15 "Halaman publik", PROMPT 18a task 6):
 *  password gate, expired/disabled states, a file card with preview and
 *  download, or the read-only folder browser. The public scope makes the
 *  viewers, thumbnails and uploads use the guest API. */
export function PublicPage({ token, path, viewPath }: PublicPageProps) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  // synchronous: children must see the scope on their first render
  const meta = useQuery({
    queryKey: publicMetaKey(token),
    queryFn: ({ signal }) => getPublicMeta(token, signal),
    retry: false,
    staleTime: 30_000,
  })
  // Step 23e: the meta says whether content lives on a separate origin
  const contentBaseUrl = meta.data?.contentBaseUrl ?? ''
  useLayoutEffect(() => {
    setPublicScope(contentBaseUrl ? { token, contentBaseUrl } : { token })
    return () => setPublicScope(null)
  }, [token, contentBaseUrl])
  const splat = (p: string) => (p === '/' ? '' : p.replace(/^\//, ''))
  const go = useCallback(
    (folder: string, view?: string) =>
      void navigate({
        to: '/s/$token/$',
        params: { token, _splat: splat(folder) },
        search: view ? { view } : {},
      }),
    [navigate, token],
  )

  if (meta.isPending) {
    return (
      <PublicLayout>
        <PageSpinner />
      </PublicLayout>
    )
  }
  if (meta.isError) {
    return (
      <PublicLayout>
        <PublicError error={meta.error} />
      </PublicLayout>
    )
  }
  const m = meta.data
  if (m.passwordRequired && !m.unlocked) {
    return (
      <PublicLayout>
        <PasswordForm
          token={token}
          onUnlocked={() => void qc.invalidateQueries({ queryKey: publicMetaKey(token) })}
        />
      </PublicLayout>
    )
  }
  return (
    <PublicLayout>
      <PublicHeader meta={m} />
      {m.isDir ? (
        <PublicBrowser token={token} meta={m} path={path} viewPath={viewPath} onNavigate={go} />
      ) : (
        <PublicFileCard meta={m} onPreview={() => go('/', '/')} />
      )}
      {!m.isDir && viewPath ? (
        <ViewerOverlay
          path="/"
          items={[]}
          paging={{ hasNextPage: false, isFetchingNextPage: false, fetchNextPage: () => {} }}
          onClose={() => go('/')}
          onNavigate={() => {}}
        />
      ) : null}
    </PublicLayout>
  )
}

function PublicHeader({ meta }: { meta: PublicMeta }) {
  const { t, i18n } = useTranslation('share')
  const lang = i18n.resolvedLanguage ?? 'id'
  return (
    <header className="mb-4 space-y-1" data-testid="public-header">
      <h1 className="truncate text-xl font-semibold" data-testid="public-name">
        {meta.name}
      </h1>
      <p className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-text-muted">
        {meta.owner ? <span data-testid="public-owner">{t('public.owner', { name: meta.owner })}</span> : null}
        {meta.expiresAt ? (
          <span className="inline-flex items-center gap-1">
            <Clock className="size-3" aria-hidden="true" />
            {t('public.expiresAt', { date: formatDate(meta.expiresAt, lang, { time: true }) })}
          </span>
        ) : null}
        {meta.maxDownloads ? (
          <span>{t('public.downloadsLeft', { n: Math.max(0, meta.maxDownloads - meta.downloadCount) })}</span>
        ) : null}
      </p>
      {meta.note ? (
        <p className="rounded border border-border bg-surface px-3 py-2 text-sm" data-testid="public-note">
          {meta.note}
        </p>
      ) : null}
      {meta.downloadLimitReached ? (
        <p role="status" className="flex items-center gap-2 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-sm">
          <AlertTriangle className="size-4" aria-hidden="true" />
          {t('public.limitReached')}
        </p>
      ) : null}
    </header>
  )
}

/** A shared file: icon, size, date, preview and download. */
function PublicFileCard({ meta, onPreview }: { meta: PublicMeta; onPreview: () => void }) {
  const { t, i18n } = useTranslation('share')
  const lang = i18n.resolvedLanguage ?? 'id'
  const previewable = opensInViewer({
    name: meta.name ?? '',
    mime: meta.mime ?? null,
    category: meta.category ?? 'other',
    ext: '',
    isDir: false,
  })
  const canDownload = meta.allowDownload && !meta.downloadLimitReached
  return (
    <section className="rounded-lg border border-border bg-surface p-4" data-testid="public-file">
      <div className="flex items-center gap-3">
        <FileIcon category={meta.category ?? 'other'} size={40} />
        <div className="min-w-0 flex-1 text-sm">
          <p className="truncate font-medium">{meta.name}</p>
          <p className="text-xs text-text-muted">
            {formatBytes(meta.size ?? 0, lang)}
            {meta.mtime ? ` · ${formatDate(meta.mtime, lang, { time: true })}` : ''}
          </p>
        </div>
      </div>
      <div className="mt-4 flex flex-wrap gap-2">
        {previewable ? (
          <Button variant="outline" onClick={onPreview} data-testid="public-preview">
            <Eye aria-hidden="true" />
            {t('public.preview')}
          </Button>
        ) : null}
        {canDownload ? (
          <Button
            onClick={() => void signDownloadUrl('/').then((u) => startDownload(u, meta.name))}
            data-testid="public-download"
          >
            <Download aria-hidden="true" />
            {t('public.download')}
          </Button>
        ) : null}
      </div>
    </section>
  )
}

/** 404 / 410 states of a link (SPEC §8.15 "expired/disabled"). */
function PublicError({ error }: { error: unknown }) {
  const { t } = useTranslation('share')
  if (isApiError(error, 'SHARE_EXPIRED')) {
    return <EmptyState icon={Clock} title={t('public.expired')} description={t('public.expiredBody')} />
  }
  if (isApiError(error, 'SHARE_DISABLED') || isApiError(error, 'SHARE_LIMIT_REACHED')) {
    const reasons = {
      manual: 'public.disabledReason.manual',
      item_trashed: 'public.disabledReason.item_trashed',
      admin_revoked: 'public.disabledReason.admin_revoked',
      owner_disabled: 'public.disabledReason.owner_disabled',
      broken: 'public.disabledReason.broken',
      locked: 'public.disabledReason.locked',
    } as const
    const reason = String(error.details?.reason ?? 'manual') as keyof typeof reasons
    return (
      <EmptyState
        icon={Ban}
        title={t('public.disabled')}
        description={t(reasons[reason] ?? reasons.manual)}
      />
    )
  }
  if (isApiError(error, 'NOT_FOUND')) {
    return <EmptyState icon={FileQuestion} title={t('public.notFound')} description={t('public.notFoundBody')} />
  }
  return <EmptyState icon={AlertTriangle} title={t('public.error')} description={errorMessage(error)} />
}
