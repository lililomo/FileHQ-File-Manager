import { describe, expect, it } from 'vitest'
import type { Item, ListResponse } from './api'
import { mergeItems } from './useListing'

function item(path: string): Item {
  return {
    name: path.split('/').pop() ?? '',
    path,
    type: 'file',
    isDir: false,
    isSymlink: false,
    size: 1,
    itemCount: null,
    mime: null,
    typeLabel: 'type.other',
    category: 'other',
    ext: '',
    mtime: '2026-10-14T00:00:00Z',
    ctime: '2026-10-14T00:00:00Z',
    btime: null,
    atime: '2026-10-14T00:00:00Z',
    mode: 'rw-r-----',
    modeOctal: '640',
    note: null,
    tag: null,
    favorite: false,
    pinned: false,
    shared: 'none',
    media: null,
    thumb: false,
    etag: 'x',
  }
}
function page(paths: string[], next: string | null): ListResponse {
  return {
    items: paths.map(item),
    nextCursor: next,
    total: 0,
    path: '/',
    parentPath: null,
    dir: { path: '/', etag: 'e', mtime: '', itemCount: paths.length },
  }
}

describe('mergeItems', () => {
  it('returns the single page as-is', () => {
    const p = page(['/a', '/b'], null)
    expect(mergeItems([p])).toBe(p.items)
  })
  it('concatenates pages and drops duplicates by path', () => {
    const merged = mergeItems([page(['/a', '/b'], 'c1'), page(['/b', '/c'], null)])
    expect(merged.map((i) => i.path)).toEqual(['/a', '/b', '/c'])
  })
  it('handles undefined', () => {
    expect(mergeItems(undefined)).toEqual([])
  })
})
