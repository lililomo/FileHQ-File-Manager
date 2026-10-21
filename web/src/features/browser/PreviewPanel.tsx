import { Download, Eye, Pencil, Share2, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { formatBytes, formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { Item } from './api'
import { TextHead } from '@/features/viewer/text/TextHead'
import { opensInViewer, viewerFor } from '@/features/viewer/viewers'
import { ItemFacts, ItemHeader } from './ItemDetails'
import { useItemStat } from './useItemStat'
import { Thumb } from './views/Thumb'

export interface PreviewPanelProps {
  items: Item[]
  /** total count of the selection (may exceed loaded items in "all" mode) */
  count: number
  onClose: () => void
  /** Overlay (< 1280 px) instead of a docked column. */
  overlay?: boolean
  /** Step 12b: download the selection (single file → signed URL, else zip). */
  onDownload?: (() => void) | undefined
  /** Step 17a: open the single selected file in the viewer overlay. */
  onOpen?: ((item: Item) => void) | undefined
}

const TEXT_KINDS: ReadonlySet<string> = new Set(['text', 'html', 'markdown', 'json', 'table'])

/** Right-hand Preview/Details panel (SPEC §9.3, PROMPT 7b task 8). */
export function PreviewPanel({
  items,
  count,
  onClose,
  overlay = false,
  onDownload,
  onOpen,
}: PreviewPanelProps) {
  const { t, i18n } = useTranslation('files')
  const lang = i18n.resolvedLanguage ?? 'id'
  const single = items.length === 1 && count === 1 ? items[0]! : null
  const stat = useItemStat(single?.path ?? null)
  const bytes = items.reduce((n, it) => n + (it.isDir ? 0 : it.size), 0)
  const soon = t('toolbar.comingSoon')
  const downloadButton = onDownload ? (
    <Button
      key="download"
      variant="outline"
      size="sm"
      className="gap-1"
      onClick={onDownload}
      data-testid="preview-download"
    >
      <Download aria-hidden="true" />
      {t('preview.download')}
    </Button>
  ) : (
    quick(t('preview.download'), Download)
  )
  function quick(label: string, Icon: typeof Download) {
    return (
      <Tooltip key={label}>
        <TooltipTrigger asChild>
          <span tabIndex={-1} className="inline-flex">
            <Button variant="outline" size="sm" disabled className="gap-1">
              <Icon aria-hidden="true" />
              {label}
            </Button>
          </span>
        </TooltipTrigger>
        <TooltipContent>
          {label} · {soon}
        </TooltipContent>
      </Tooltip>
    )
  }
  return (
    <aside
      className={cn(
        'flex w-80 shrink-0 flex-col border-l border-border bg-surface',
        overlay && 'absolute inset-y-0 right-0 z-20 shadow-lg',
      )}
      aria-label={t('preview.label')}
      data-testid="preview-panel"
    >
      <div className="flex h-9 items-center justify-between border-b border-border px-3 text-xs font-medium text-text-muted">
        <span>{t('preview.title')}</span>
        <IconButton label={t('preview.close')} shortcut="Alt+P" size="icon-sm" onClick={onClose}>
          <X aria-hidden="true" />
        </IconButton>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-3">
        {single ? (
          <div className="space-y-4">
            {single.thumb && !single.isDir ? (
              <div className="flex justify-center rounded-md bg-surface-2 p-2">
                <Thumb item={single} box={256} className="rounded" />
              </div>
            ) : !single.isDir && TEXT_KINDS.has(viewerFor(single)) ? (
              // Step 17b: the same compact text preview as Quick Look
              <div className="h-56 overflow-hidden rounded-md border border-border">
                <TextHead path={single.path} etag={single.etag} lines={40} />
              </div>
            ) : null}
            <ItemHeader item={single} />
            <div className="flex flex-wrap gap-1">
              {onOpen && opensInViewer(single) ? (
                <Button
                  variant="primary"
                  size="sm"
                  className="gap-1"
                  onClick={() => onOpen(single)}
                  data-testid="preview-open"
                >
                  <Eye aria-hidden="true" />
                  {t('preview.open')}
                </Button>
              ) : null}
              {downloadButton}
              {quick(t('preview.edit'), Pencil)}
              {quick(t('preview.share'), Share2)}
            </div>
            <ItemFacts
              item={single}
              stat={stat.data?.stat}
              versionsCount={stat.data?.versionsCount}
            />
          </div>
        ) : count > 0 ? (
          <div className="space-y-2 text-sm">
            <div className="font-medium">
              {t('preview.multi', { count, n: formatNumber(count, lang) })}
            </div>
            <div className="text-xs text-text-muted">
              {t('preview.multiSize', { size: formatBytes(bytes, lang) })}
              {items.length < count ? ` ${t('preview.loadedOnly')}` : ''}
            </div>
            <div className="flex flex-wrap gap-1">{downloadButton}</div>
          </div>
        ) : (
          <p className="text-xs text-text-muted">{t('preview.empty')}</p>
        )}
      </div>
    </aside>
  )
}
