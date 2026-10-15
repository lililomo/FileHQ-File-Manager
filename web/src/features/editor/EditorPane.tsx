import { useTranslation } from 'react-i18next'
import { AlertTriangle, Download, FileWarning, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { PageSpinner } from '@/components/PageSpinner'
import { formatBytes, formatDateTimeFull, formatNumber } from '@/lib/format'
import type { EditorTab } from '@/stores/editor'
import { useSessionStore } from '@/stores/session'
import { livePathOf, useWorkspace } from './controller'
import { MarkdownPreview } from './MarkdownPreview'
import { MonacoHost } from './MonacoHost'

/** The editing area of the active tab: banners (tail mode, draft, lossy,
 *  conflict) above Monaco; binary/error states replace it. */
export function EditorPane({ tab }: { tab: EditorTab }) {
  const { t, i18n } = useTranslation('editor')
  const ws = useWorkspace()
  const maxSize = useSessionStore((s) => s.config?.editorMaxFileSize ?? 0)
  if (tab.status === 'loading' && !tab.etag) return <PageSpinner />
  if (tab.status === 'binary') {
    return (
      <Notice icon={<FileWarning className="size-8 text-text-muted" />} text={t('banner.binary')}>
        <Button variant="outline" onClick={() => void ws.download(tab.path)}>
          <Download /> {t('banner.download')}
        </Button>
      </Notice>
    )
  }
  if (tab.status === 'error') {
    const notFound = tab.error?.code === 'NOT_FOUND'
    return (
      <Notice
        icon={<AlertTriangle className="size-8 text-destructive" />}
        text={
          notFound
            ? t('banner.notFound')
            : t('banner.loadError', { message: tab.error?.message ?? '' })
        }
      >
        <Button variant="outline" onClick={() => void ws.load(tab.path)}>
          <RefreshCw /> {t('banner.retry')}
        </Button>
      </Notice>
    )
  }
  const lang = i18n.resolvedLanguage ?? 'id'
  const livePath = livePathOf(tab.path)
  const showPreview = tab.preview && tab.language === 'markdown' && tab.status === 'ready'
  return (
    <div className="flex h-full min-h-0 flex-col">
      {tab.versionId ? (
        <Banner tone="info" testId="banner-version">
          <span>
            {t('banner.versionTab', {
              no: tab.versionNo,
              at: formatDateTimeFull(tab.mtime, lang),
            })}
          </span>
          <Button
            size="xs"
            variant="outline"
            onClick={() =>
              void ws.compareVersion(livePath, {
                id: tab.versionId!,
                no: tab.versionNo,
                path: livePath,
                size: tab.size,
                sha256: '',
                reason: 'save',
                createdAt: tab.mtime,
              })
            }
            data-testid="version-tab-compare"
          >
            {t('banner.versionCompare')}
          </Button>
          <Button
            size="xs"
            onClick={() => ws.openVersions(livePath)}
            data-testid="version-tab-history"
          >
            {t('toolbar.history')}
          </Button>
        </Banner>
      ) : null}
      {tab.truncated ? (
        <Banner tone="warn" testId="banner-tail">
          <span>
            {t('banner.tail', { n: formatNumber(tab.tailLines, lang) })}{' '}
            <span className="text-text-muted" data-testid="tail-reason">
              {t('banner.tailReason', {
                max: formatBytes(maxSize, lang),
                n: formatNumber(tab.tailLines, lang),
              })}
            </span>
          </span>
          <Button
            size="xs"
            variant="outline"
            onClick={() => ws.loadMoreTail(tab.path)}
            data-testid="tail-more"
          >
            {t('banner.loadMore')}
          </Button>
          <Button
            size="xs"
            variant="ghost"
            onClick={() => void ws.download(tab.path)}
            data-testid="tail-download"
          >
            <Download /> {t('banner.downloadToEdit')}
          </Button>
        </Banner>
      ) : null}
      {tab.draft ? (
        <Banner tone="info" testId="banner-draft">
          <span>
            {tab.draft.etag === tab.etag ? t('banner.draftFound') : t('banner.draftChanged')}
          </span>
          <Button size="xs" onClick={() => ws.restoreDraft(tab.path)} data-testid="draft-restore">
            {t('banner.draftRestore')}
          </Button>
          <Button size="xs" variant="outline" onClick={() => void ws.viewDraftDiff(tab.path)}>
            {t('banner.draftView')}
          </Button>
          <Button
            size="xs"
            variant="ghost"
            onClick={() => void ws.discardDraft(tab.path)}
            data-testid="draft-discard"
          >
            {t('banner.draftDiscard')}
          </Button>
        </Banner>
      ) : null}
      {tab.lossy ? (
        <Banner tone="warn" testId="banner-lossy">
          <span>{t('status.lossy')}</span>
        </Banner>
      ) : null}
      <div className="flex min-h-0 flex-1">
        <div className={showPreview ? 'min-w-0 flex-1 basis-1/2' : 'min-w-0 flex-1'}>
          <MonacoHost tab={tab} />
        </div>
        {showPreview ? (
          <div className="min-w-0 flex-1 basis-1/2">
            <MarkdownPreview path={tab.path} />
          </div>
        ) : null}
      </div>
    </div>
  )
}

function Banner({
  tone,
  testId,
  children,
}: {
  tone: 'warn' | 'info'
  testId: string
  children: React.ReactNode
}) {
  return (
    <div
      role="status"
      data-testid={testId}
      className={
        tone === 'warn'
          ? 'flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/10 px-3 py-1.5 text-xs text-text'
          : 'flex flex-wrap items-center gap-2 border-b border-accent/40 bg-accent/10 px-3 py-1.5 text-xs text-text'
      }
    >
      {children}
    </div>
  )
}

function Notice({
  icon,
  text,
  children,
}: {
  icon: React.ReactNode
  text: string
  children?: React.ReactNode
}) {
  return (
    <div
      className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-sm text-text-muted"
      data-testid="editor-notice"
    >
      {icon}
      <p>{text}</p>
      {children}
    </div>
  )
}
