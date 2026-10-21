import { useEffect, useMemo } from 'react'

/** Prev/next among the files of the same viewer kind in the loaded listing
 *  (SPEC §8.14 "navigasi prev/next ke file sejenis di folder yang sama").
 *  The order is the listing order (= the active sort); when the current file
 *  is near the end of what is loaded and the folder has more pages, the next
 *  page is requested so "next" keeps working past the first 500 items. */
export interface Siblings<T> {
  /** files of the same kind, in listing order (includes the current one when loaded) */
  list: T[]
  /** index of the current path in `list`, −1 when not loaded (deep link beyond the loaded pages) */
  index: number
  prev: T | null
  next: T | null
  total: number
}

export function siblingsOf<T extends { path: string }>(
  items: readonly T[],
  path: string,
  same: (item: T) => boolean,
): Siblings<T> {
  const list = items.filter((it) => it.path === path || same(it))
  const index = list.findIndex((it) => it.path === path)
  return {
    list,
    index,
    prev: index > 0 ? list[index - 1]! : null,
    next: index >= 0 && index < list.length - 1 ? list[index + 1]! : null,
    total: list.length,
  }
}

/** How many loaded siblings must remain after the current one before the
 *  next listing page is fetched. */
export const PREFETCH_MARGIN = 3

export interface SiblingNavPaging {
  hasNextPage: boolean
  isFetchingNextPage: boolean
  fetchNextPage: () => void
}

export function useSiblingNav<T extends { path: string }>(
  items: readonly T[],
  path: string,
  same: (item: T) => boolean,
  paging: SiblingNavPaging,
): Siblings<T> {
  const siblings = useMemo(() => siblingsOf(items, path, same), [items, path, same])
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = paging
  const nearEnd = siblings.index < 0 || siblings.list.length - siblings.index <= PREFETCH_MARGIN
  useEffect(() => {
    if (nearEnd && hasNextPage && !isFetchingNextPage) fetchNextPage()
  }, [nearEnd, hasNextPage, isFetchingNextPage, fetchNextPage])
  return siblings
}
