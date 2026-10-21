import { Download, FolderSearch } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Button } from '@/components/ui/button'
import type { Item } from '@/features/browser/api'
import { typeLabel } from '@/features/browser/columns'
import { formatBytes, formatDateTimeFull } from '@/lib/format'
import type { ViewerKind } from './viewers'

const OFFICE_EXT = new Set([
  'doc',
  'docx',
  'xls',
  'xlsx',
  'ppt',
  'pptx',
  'odt',
  'ods',
  'odp',
  'rtf',
])

/** Fallback body of the overlay (SPEC §8.14 "InfoCard"): icon, name,
 *  type, size, date and a download button — Office documents (no preview
 *  until a later step), archives (button to the archive browser) and
 *  unknown types. */
export function InfoCard({
  item,
  kind,
  onDownload,
  onBrowseArchive,
}: {
  item: Item
  kind: ViewerKind
  onDownload: () => void
  onBrowseArchive?: (() => void) | undefined
}) {
  const { t, i18n } = useTranslation(['viewer', 'files'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const office = OFFICE_EXT.has((item.ext || '').toLowerCase())
  const message =
    kind === 'archive'
      ? t('viewer:card.archive')
      : office
        ? t('viewer:card.office')
        : t('viewer:card.unsupported')
  return (
    <div
      className="flex h-full w-full items-center justify-center p-6"
      data-testid="viewer-info-card"
    >
      <div className="flex max-w-md flex-col items-center gap-3 rounded-lg border border-border bg-surface p-6 text-center">
        <FileIcon category={toIconCategory(item.category)} size={64} />
        <div className="min-w-0 max-w-full">
          <div className="truncate text-base font-medium text-text" title={item.name}>
            {item.name}
          </div>
          <div className="text-xs text-text-muted">
            {typeLabel(t as unknown as Parameters<typeof typeLabel>[0], item)} ·{' '}
            {formatBytes(item.size, lang)} · {formatDateTimeFull(item.mtime, lang)}
          </div>
        </div>
        <p className="text-sm text-text-muted">{message}</p>
        <div className="flex flex-wrap justify-center gap-2">
          {kind === 'archive' && onBrowseArchive ? (
            <Button variant="outline" onClick={onBrowseArchive} data-testid="viewer-card-archive">
              <FolderSearch aria-hidden="true" />
              {t('viewer:card.browseArchive')}
            </Button>
          ) : null}
          <Button variant="primary" onClick={onDownload} data-testid="viewer-card-download">
            <Download aria-hidden="true" />
            {t('viewer:card.download')}
          </Button>
        </div>
      </div>
    </div>
  )
}
