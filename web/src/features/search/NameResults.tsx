import { useVirtualizer } from '@tanstack/react-virtual'
import { FolderOpen } from 'lucide-react'
import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Button } from '@/components/ui/button'
import { ItemBadges } from '@/features/browser/Badges'
import { useOpenLocation } from '@/features/browser/openLocation'
import { formatBytes, formatDateTimeFull, formatRelativeTime } from '@/lib/format'
import type { NameItem } from './api'

const ROW = 32

/** Name results: a virtualised table with the Location column and
 *  "Buka lokasi file" (SPEC §8.12; same row model as Recent). */
export function NameResults({
  items,
  hasMore,
  loadingMore,
  onLoadMore,
}: {
  items: NameItem[]
  hasMore: boolean
  loadingMore: boolean
  onLoadMore: () => void
}) {
  const { t, i18n } = useTranslation(['search'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const openLocation = useOpenLocation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW,
    overscan: 12,
  })
  return (
    <div className="flex min-h-0 flex-1 flex-col" role="table" aria-label={t('search:tabs.name')}>
      <div
        className="flex items-center gap-2 border-b border-border bg-surface-2 px-3 py-1.5 text-xs font-medium text-text-muted"
        role="row"
      >
        <span className="min-w-0 flex-1" role="columnheader">
          {t('search:results.columns.name')}
        </span>
        <span className="hidden w-72 md:block" role="columnheader">
          {t('search:results.columns.location')}
        </span>
        <span className="hidden w-28 lg:block" role="columnheader">
          {t('search:results.columns.mtime')}
        </span>
        <span className="hidden w-20 text-right sm:block" role="columnheader">
          {t('search:results.columns.size')}
        </span>
        <span className="w-9" />
      </div>
      <div ref={scrollRef} className="min-h-0 flex-1 overflow-auto" data-testid="search-name-body">
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((v) => {
            const it = items[v.index]!
            return (
              <div
                key={it.path}
                className="absolute inset-x-0 flex items-center gap-2 border-b border-border/60 px-3 text-sm hover:bg-surface-2"
                style={{ height: ROW, transform: `translateY(${v.start}px)` }}
                role="row"
                data-testid="search-name-row"
                data-path={it.path}
                onDoubleClick={() => openLocation(it)}
              >
                <span className="flex min-w-0 flex-1 items-center gap-2" role="cell">
                  <FileIcon
                    category={it.isDir ? 'folder' : toIconCategory(it.category)}
                    size={16}
                  />
                  <button
                    type="button"
                    className="truncate text-left hover:underline"
                    onClick={() => openLocation(it, true)}
                    title={it.name}
                  >
                    {it.name}
                  </button>
                  <ItemBadges item={it} />
                </span>
                <span
                  className="hidden w-72 truncate font-mono text-xs text-text-muted md:block"
                  role="cell"
                  title={it.location}
                >
                  {it.location}
                </span>
                <span
                  className="hidden w-28 text-xs tabular text-text-muted lg:block"
                  role="cell"
                  title={formatDateTimeFull(it.mtime, lang)}
                >
                  {formatRelativeTime(it.mtime, lang)}
                </span>
                <span
                  className="hidden w-20 text-right text-xs tabular text-text-muted sm:block"
                  role="cell"
                >
                  {it.isDir ? '—' : formatBytes(it.size, lang)}
                </span>
                <span className="flex w-9 justify-end" role="cell">
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={t('search:results.openLocation')}
                    onClick={() => openLocation(it)}
                    data-testid="search-open-location"
                  >
                    <FolderOpen aria-hidden="true" />
                  </Button>
                </span>
              </div>
            )
          })}
        </div>
        {hasMore ? (
          <div className="flex justify-center p-3">
            <Button
              size="sm"
              variant="outline"
              disabled={loadingMore}
              onClick={onLoadMore}
              data-testid="search-load-more"
            >
              {t('search:results.loadMore')}
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  )
}
