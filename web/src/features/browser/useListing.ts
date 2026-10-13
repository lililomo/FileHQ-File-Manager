import { useInfiniteQuery, type InfiniteData } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { listDir, listQueryKey, type Item, type ListOptions, type ListResponse } from './api'

/** Flatten cursor pages into one list, dropping duplicates by path (a page
 *  refetched after an insert may overlap the next one). */
export function mergeItems(pages: ListResponse[] | undefined): Item[] {
  if (!pages || pages.length === 0) return []
  if (pages.length === 1) return pages[0]!.items
  const seen = new Set<string>()
  const out: Item[] = []
  for (const page of pages) {
    for (const it of page.items) {
      if (seen.has(it.path)) continue
      seen.add(it.path)
      out.push(it)
    }
  }
  return out
}

export function useListing(path: string, opts: ListOptions = {}) {
  const { i18n } = useTranslation()
  const key = listQueryKey(path, opts)
  const query = useInfiniteQuery<
    ListResponse,
    Error,
    InfiniteData<ListResponse, string | null>,
    typeof key,
    string | null
  >({
    queryKey: key,
    queryFn: ({ pageParam, signal }) => listDir(path, opts, pageParam, signal),
    initialPageParam: null,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    // Locale affects server collation, refetch when it changes.
    meta: { lang: i18n.resolvedLanguage },
  })
  const pages = query.data?.pages
  const items = useMemo(() => mergeItems(pages), [pages])
  const first = pages?.[0]
  return {
    ...query,
    items,
    total: first?.total ?? 0,
    dir: first?.dir,
    parentPath: first?.parentPath ?? null,
  }
}
