import { renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { PREFETCH_MARGIN, siblingsOf, useSiblingNav } from './useSiblingNav'

type F = { path: string; kind: string }
const f = (path: string, kind = 'image'): F => ({ path, kind })
const isImage = (it: F) => it.kind === 'image'

describe('siblingsOf', () => {
  const items = [f('/a.jpg'), f('/b.pdf', 'pdf'), f('/c.png'), f('/d.txt', 'text'), f('/e.gif')]
  it('keeps listing order and skips other kinds', () => {
    const s = siblingsOf(items, '/c.png', isImage)
    expect(s.list.map((i) => i.path)).toEqual(['/a.jpg', '/c.png', '/e.gif'])
    expect(s.index).toBe(1)
    expect(s.prev?.path).toBe('/a.jpg')
    expect(s.next?.path).toBe('/e.gif')
    expect(s.total).toBe(3)
  })
  it('has no prev at the start and no next at the end', () => {
    expect(siblingsOf(items, '/a.jpg', isImage).prev).toBeNull()
    expect(siblingsOf(items, '/e.gif', isImage).next).toBeNull()
  })
  it('reports index −1 when the file is not loaded (deep link past the first page)', () => {
    const s = siblingsOf(items, '/zz.jpg', isImage)
    expect(s.index).toBe(-1)
    expect(s.prev).toBeNull()
    expect(s.next).toBeNull()
  })
  it('always includes the current file even when the predicate rejects it', () => {
    const s = siblingsOf(items, '/d.txt', isImage)
    expect(s.list.map((i) => i.path)).toEqual(['/a.jpg', '/c.png', '/d.txt', '/e.gif'])
    expect(s.index).toBe(2)
  })
})

describe('useSiblingNav paging', () => {
  it('fetches the next page when the current file is near the loaded end', () => {
    const items = Array.from({ length: 10 }, (_, i) => f(`/${i}.jpg`))
    const fetchNextPage = vi.fn()
    renderHook(() =>
      useSiblingNav(items, `/${10 - PREFETCH_MARGIN}.jpg`, isImage, {
        hasNextPage: true,
        isFetchingNextPage: false,
        fetchNextPage,
      }),
    )
    expect(fetchNextPage).toHaveBeenCalledTimes(1)
  })
  it('does not fetch in the middle of the list or without further pages', () => {
    const items = Array.from({ length: 10 }, (_, i) => f(`/${i}.jpg`))
    const fetchNextPage = vi.fn()
    renderHook(() =>
      useSiblingNav(items, '/2.jpg', isImage, {
        hasNextPage: true,
        isFetchingNextPage: false,
        fetchNextPage,
      }),
    )
    renderHook(() =>
      useSiblingNav(items, '/9.jpg', isImage, {
        hasNextPage: false,
        isFetchingNextPage: false,
        fetchNextPage,
      }),
    )
    expect(fetchNextPage).not.toHaveBeenCalled()
  })
})
