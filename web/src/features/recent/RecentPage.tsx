import { useQuery } from '@tanstack/react-query'
import { Clock, FolderOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/EmptyState'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ItemBadges } from '@/features/browser/Badges'
import { useOpenLocation } from '@/features/browser/openLocation'
import { formatBytes, formatDateTimeFull, formatRelativeTime } from '@/lib/format'
import { listRecent, RECENT_QUERY_KEY } from './api'

/** `/recent` (SPEC §8.7): files touched in the last 30 days with their
 *  location and "Buka lokasi file" (navigate + select). */
export function RecentPage() {
  const { t, i18n } = useTranslation(['meta', 'files'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const openLocation = useOpenLocation()
  const query = useQuery({
    queryKey: RECENT_QUERY_KEY,
    queryFn: ({ signal }) => listRecent(100, signal),
    staleTime: 10_000,
  })
  const items = query.data?.items ?? []
  return (
    <div className="flex h-full flex-col" data-testid="recent-page">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold">{t('meta:recent.title')}</h1>
        <p className="w-full text-xs text-text-muted sm:ml-2 sm:w-auto">
          {t('meta:recent.subtitle', { days: query.data?.windowDays ?? 30 })}
        </p>
      </header>
      {query.isPending ? (
        <div className="space-y-2 p-3" aria-busy="true">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          icon={Clock}
          title={t('meta:recent.empty')}
          description={t('meta:recent.emptyHint')}
          className="h-full"
        />
      ) : (
        <div
          className="min-h-0 flex-1 overflow-auto"
          role="table"
          aria-label={t('meta:recent.title')}
        >
          <div
            className="sticky top-0 z-10 flex items-center gap-2 border-b border-border bg-surface-2 px-3 py-1.5 text-xs font-medium text-text-muted"
            role="row"
          >
            <span className="min-w-0 flex-1" role="columnheader">
              {t('meta:recent.columns.name')}
            </span>
            <span className="hidden w-64 md:block" role="columnheader">
              {t('meta:recent.columns.location')}
            </span>
            <span className="hidden w-24 lg:block" role="columnheader">
              {t('meta:recent.columns.action')}
            </span>
            <span className="w-28" role="columnheader">
              {t('meta:recent.columns.at')}
            </span>
            <span className="hidden w-20 text-right sm:block" role="columnheader">
              {t('meta:recent.columns.size')}
            </span>
            <span className="w-10" />
          </div>
          {items.map((it) => (
            <div
              key={it.path}
              className="flex items-center gap-2 border-b border-border/60 px-3 py-1.5 text-sm hover:bg-surface-2"
              role="row"
              data-testid="recent-row"
              data-path={it.path}
              onDoubleClick={() => openLocation(it)}
            >
              <span className="flex min-w-0 flex-1 items-center gap-2" role="cell">
                <FileIcon category={it.isDir ? 'folder' : toIconCategory(it.category)} size={16} />
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
                className="hidden w-64 truncate font-mono text-xs text-text-muted md:block"
                role="cell"
                title={it.location}
              >
                {it.location}
              </span>
              <span className="hidden w-24 text-xs text-text-muted lg:block" role="cell">
                {t(`meta:recent.actions.${it.action}`)}
              </span>
              <span
                className="w-28 text-xs tabular text-text-muted"
                role="cell"
                title={formatDateTimeFull(it.at, lang)}
              >
                {formatRelativeTime(it.at, lang)}
              </span>
              <span
                className="hidden w-20 text-right text-xs tabular text-text-muted sm:block"
                role="cell"
              >
                {it.isDir ? '—' : formatBytes(it.size, lang)}
              </span>
              <span className="flex w-10 justify-end" role="cell">
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t('meta:recent.openLocation')}
                  onClick={() => openLocation(it)}
                  data-testid="recent-open-location"
                >
                  <FolderOpen aria-hidden="true" />
                </Button>
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
