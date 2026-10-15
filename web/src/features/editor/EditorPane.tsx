import { useTranslation } from 'react-i18next'
import { AlertTriangle, Download, FileWarning, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { PageSpinner } from '@/components/PageSpinner'
import { formatNumber } from '@/lib/format'
import type { EditorTab } from '@/stores/editor'
import { useWorkspace } from './controller'
import { MonacoHost } from './MonacoHost'

/** The editing area of the active tab: banners (tail mode, draft, lossy,
 *  conflict) above Monaco; binary/error states replace it. */
export function EditorPane({ tab }: { tab: EditorTab }) {
  const { t, i18n } = useTranslation('editor')
  const ws = useWorkspace()
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
  return (
    <div className="flex h-full min-h-0 flex-col">
      {tab.truncated ? (
        <Banner tone="warn" testId="banner-tail">
          <span>
            {t('banner.tail', { n: formatNumber(tab.tailLines, i18n.resolvedLanguage ?? 'id') })}
          </span>
          <Button
            size="xs"
            variant="outline"
            onClick={() => ws.loadMoreTail(tab.path)}
            data-testid="tail-more"
          >
            {t('banner.loadMore')}
          </Button>
          <Button size="xs" variant="ghost" onClick={() => void ws.download(tab.path)}>
            <Download /> {t('banner.download')}
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
      <div className="min-h-0 flex-1">
        <MonacoHost tab={tab} />
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
