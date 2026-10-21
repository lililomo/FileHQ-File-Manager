import { Download } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Button } from '@/components/ui/button'
import type { Item } from '@/features/browser/api'
import { typeLabel } from '@/features/browser/columns'
import { formatBytes, formatDateTimeFull } from '@/lib/format'
import type { ViewerKind } from './viewers'

/** Fallback body of the overlay (SPEC §8.14 "InfoCard"): icon, name,
 *  type, size, date and a download button — for kinds without a viewer
 *  (Office, unknown binaries) and, until Step 17b, audio/video/text. */
export function InfoCard({
  item,
  kind,
  onDownload,
}: {
  item: Item
  kind: ViewerKind
  onDownload: () => void
}) {
  const { t, i18n } = useTranslation(['viewer', 'files'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const pending = kind !== 'info' && kind !== 'image' && kind !== 'pdf'
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
        <p className="text-sm text-text-muted">
          {pending ? t('viewer:card.comingSoon') : t('viewer:card.unsupported')}
        </p>
        <Button variant="primary" onClick={onDownload} data-testid="viewer-card-download">
          <Download aria-hidden="true" />
          {t('viewer:card.download')}
        </Button>
      </div>
    </div>
  )
}
