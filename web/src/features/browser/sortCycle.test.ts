import { describe, expect, it } from 'vitest'
import { DEFAULT_SORT, cycleSort, normalizeSort } from './sortCycle'

describe('cycleSort', () => {
  it('cycles asc → desc → default on the primary field', () => {
    const a = cycleSort(DEFAULT_SORT, 'size')
    expect(a).toEqual({ field: 'size', dir: 'asc' })
    const b = cycleSort(a, 'size')
    expect(b).toEqual({ field: 'size', dir: 'desc' })
    expect(cycleSort(b, 'size')).toEqual(DEFAULT_SORT)
  })
  it('keeps the secondary when the primary changes, drops it on reset', () => {
    let s = cycleSort(DEFAULT_SORT, 'type', true)
    expect(s).toEqual({ field: 'name', dir: 'asc', field2: 'type', dir2: 'asc' })
    s = cycleSort(s, 'size')
    expect(s).toEqual({ field: 'size', dir: 'asc', field2: 'type', dir2: 'asc' })
    s = cycleSort(s, 'size')
    s = cycleSort(s, 'size')
    expect(s).toEqual(DEFAULT_SORT)
  })
  it('shift-click cycles the secondary asc → desc → none and ignores the primary field', () => {
    let s = cycleSort(DEFAULT_SORT, 'mtime', true)
    s = cycleSort(s, 'mtime', true)
    expect(s.dir2).toBe('desc')
    s = cycleSort(s, 'mtime', true)
    expect(s).toEqual(DEFAULT_SORT)
    expect(cycleSort(DEFAULT_SORT, 'name', true)).toEqual(DEFAULT_SORT)
  })
  it('normalizes stored preferences', () => {
    expect(normalizeSort(undefined)).toEqual(DEFAULT_SORT)
    expect(normalizeSort({ field: 'bogus' })).toEqual(DEFAULT_SORT)
    expect(normalizeSort({ field: 'size', dir: 'desc', field2: 'size' })).toEqual({
      field: 'size',
      dir: 'desc',
    })
    expect(normalizeSort({ field: 'size', dir: 'x', field2: 'name', dir2: 'desc' })).toEqual({
      field: 'size',
      dir: 'asc',
      field2: 'name',
      dir2: 'desc',
    })
  })
})
