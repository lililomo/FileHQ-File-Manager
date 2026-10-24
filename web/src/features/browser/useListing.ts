import { useInfiniteQuery, type InfiniteData } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
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

/** Same listing except for the `media` flag (index of `media` in listQueryKey). */
function sameExceptMedia(a: readonly unknown[] | undefined, b: readonly unknown[]): boolean {
  if (!a || a.length !== b.length) return false
  return a.every((v, i) => i === MEDIA_KEY_INDEX || v === b[i])
}
const MEDIA_KEY_INDEX = (() => {
  const plain = listQueryKey('/', {})
  return listQueryKey('/', { media: true }).findIndex((v, i) => v !== plain[i])
})()

/** Runs fn when the browser is idle (fallback: shortly after). */
function onIdle(fn: () => void): () => void {
  if (typeof window.requestIdleCallback === 'function') {
    const id = window.requestIdleCallback(fn, { timeout: 1500 })
    return () => window.cancelIdleCallback(id)
  }
  const id = window.setTimeout(fn, 200)
  return () => window.clearTimeout(id)
}

export function useListing(path: string, opts: ListOptions = {}) {
  const { i18n } = useTranslation()
  // Dimensi/Durasi (`media`) make the server probe up to 50 files of the page
  // synchronously. The first render never waits for that: the plain listing
  // comes first, the enriched one is requested when the browser is idle and
  // replaces it in place (Step 21, PROMPT "requestIdleCallback untuk enrich").
  const [enrichedPath, setEnrichedPath] = useState<string | null>(null)
  const wantMedia = !!opts.media
  const effective = { ...opts, media: wantMedia && enrichedPath === path }
  const key = listQueryKey(path, effective)
  const query = useInfiniteQuery<
    ListResponse,
    Error,
    InfiniteData<ListResponse, string | null>,
    typeof key,
    string | null
  >({
    queryKey: key,
    queryFn: ({ pageParam, signal }) => listDir(path, effective, pageParam, signal),
    initialPageParam: null,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    // keep the rows on screen while the media-enriched variant loads
    placeholderData: (prev, prevQuery) =>
      sameExceptMedia(prevQuery?.queryKey, key) ? prev : undefined,
    // Locale affects server collation, refetch when it changes.
    meta: { lang: i18n.resolvedLanguage },
  })
  const loaded = query.data !== undefined && !query.isPlaceholderData
  useEffect(() => {
    if (!wantMedia || enrichedPath === path || !loaded) return
    return onIdle(() => setEnrichedPath(path))
  }, [wantMedia, enrichedPath, path, loaded])
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
