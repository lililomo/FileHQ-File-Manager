import { useQuery } from '@tanstack/react-query'
import { FolderOpen, Link2Off, Star, StarOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/EmptyState'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ItemBadges } from '@/features/browser/Badges'
import { typeLabel } from '@/features/browser/columns'
import { locationOf, useOpenLocation } from '@/features/browser/openLocation'
import { FAVORITES_QUERY_KEY, listFavorites } from '@/features/meta/api'
import { useMeta } from '@/features/meta/useMeta'
import { formatBytes, formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

/** `/favorites` (SPEC §8.7): every favourite with its location; folders open
 *  the browser, files open their location; broken references get a badge
 *  and "Hapus dari favorit". */
export function FavoritesPage() {
  const { t, i18n } = useTranslation(['meta', 'files', 'nav'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const meta = useMeta()
  const openLocation = useOpenLocation()
  const query = useQuery({
    queryKey: FAVORITES_QUERY_KEY,
    queryFn: ({ signal }) => listFavorites(signal),
    staleTime: 10_000,
  })
  const items = query.data?.items ?? []
  return (
    <div className="flex h-full flex-col" data-testid="favorites-page">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold">{t('nav:favorites')}</h1>
        <p className="text-xs text-text-muted sm:ml-2" data-testid="favorites-count">
          {t('meta:favorite.count', { count: items.length, n: formatNumber(items.length, lang) })}
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
          icon={Star}
          title={t('meta:favorite.empty')}
          description={t('meta:favorite.emptyHint')}
          className="h-full"
        />
      ) : (
        <div
          className="min-h-0 flex-1 overflow-auto"
          role="table"
          aria-label={t('meta:badges.favorite')}
        >
          <div
            className="sticky top-0 z-10 flex items-center gap-2 border-b border-border bg-surface-2 px-3 py-1.5 text-xs font-medium text-text-muted"
            role="row"
          >
            <span className="min-w-0 flex-1" role="columnheader">
              {t('meta:favorite.columns.name')}
            </span>
            <span className="hidden w-64 md:block" role="columnheader">
              {t('meta:favorite.columns.location')}
            </span>
            <span className="hidden w-36 lg:block" role="columnheader">
              {t('meta:favorite.columns.type')}
            </span>
            <span className="w-20 text-right" role="columnheader">
              {t('meta:favorite.columns.size')}
            </span>
            <span className="w-20" />
          </div>
          {items.map((it) => (
            <div
              key={it.path}
              className={cn(
                'flex items-center gap-2 border-b border-border/60 px-3 py-1.5 text-sm hover:bg-surface-2',
                it.broken && 'opacity-80',
              )}
              role="row"
              data-testid="favorite-row"
              data-path={it.path}
              data-broken={it.broken || undefined}
              onDoubleClick={() => !it.broken && openLocation(it, true)}
            >
              <span className="flex min-w-0 flex-1 items-center gap-2" role="cell">
                <FileIcon category={it.isDir ? 'folder' : toIconCategory(it.category)} size={16} />
                <button
                  type="button"
                  className="truncate text-left hover:underline disabled:no-underline"
                  onClick={() => openLocation(it, true)}
                  disabled={it.broken}
                  title={it.name}
                >
                  {it.name}
                </button>
                {it.broken ? (
                  <span
                    className="inline-flex items-center gap-1 rounded-full bg-danger/10 px-1.5 text-[11px] text-danger"
                    data-testid="favorite-broken"
                  >
                    <Link2Off className="size-3" aria-hidden="true" />
                    {t('meta:favorite.broken')}
                  </span>
                ) : (
                  <ItemBadges item={{ ...it, favorite: false }} />
                )}
              </span>
              <span
                className="hidden w-64 truncate font-mono text-xs text-text-muted md:block"
                role="cell"
                title={locationOf(it.path)}
              >
                {locationOf(it.path)}
              </span>
              <span className="hidden w-36 truncate text-xs text-text-muted lg:block" role="cell">
                {it.broken ? '—' : typeLabel(t as never, it)}
              </span>
              <span className="w-20 text-right text-xs tabular text-text-muted" role="cell">
                {it.isDir || it.broken ? '—' : formatBytes(it.size, lang)}
              </span>
              <span className="flex w-20 justify-end gap-0.5" role="cell">
                {!it.broken ? (
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={t('meta:recent.openLocation')}
                    onClick={() => openLocation(it)}
                    data-testid="favorite-open-location"
                  >
                    <FolderOpen aria-hidden="true" />
                  </Button>
                ) : null}
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label={t('meta:favorite.removeBroken')}
                  onClick={() => void meta.removeFavorites([it.path])}
                  data-testid="favorite-remove"
                >
                  <StarOff aria-hidden="true" />
                </Button>
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
