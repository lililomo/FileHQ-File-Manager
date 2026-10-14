import type { ArchiveEntry } from './api'

export type EntrySortField = 'name' | 'size' | 'compressed' | 'ratio' | 'mtime'
export interface EntrySort {
  field: EntrySortField
  dir: 'asc' | 'desc'
}

export function ratioOf(e: ArchiveEntry): number | null {
  if (e.isDir || e.compressedSize < 0 || e.size <= 0) return null
  return Math.max(0, Math.round((1 - e.compressedSize / e.size) * 100))
}

/** Client-side order of one page (folders first; the server already
 *  delivers natural name order, other fields sort stably on top of it). */
export function sortEntries(items: ArchiveEntry[], sort: EntrySort): ArchiveEntry[] {
  const dir = sort.dir === 'asc' ? 1 : -1
  const key = (e: ArchiveEntry): number | string => {
    switch (sort.field) {
      case 'size':
        return e.size
      case 'compressed':
        return e.compressedSize
      case 'ratio':
        return ratioOf(e) ?? -1
      case 'mtime':
        return Date.parse(e.mtime) || 0
      default:
        return 0
    }
  }
  const indexed = items.map((e, i) => ({ e, i }))
  indexed.sort((a, b) => {
    if (a.e.isDir !== b.e.isDir) return a.e.isDir ? -1 : 1
    if (sort.field === 'name') return dir * (a.i - b.i)
    const ka = key(a.e)
    const kb = key(b.e)
    if (ka < kb) return -dir
    if (ka > kb) return dir
    return a.i - b.i
  })
  return indexed.map((x) => x.e)
}
