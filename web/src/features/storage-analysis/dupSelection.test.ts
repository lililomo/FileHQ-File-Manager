import { describe, expect, it } from 'vitest'
import { withoutPaths, type DuplicateGroup, type DuplicateReport } from './api'
import { dupReducer, dupTotals, initSelection, parseStorageSearch } from './dupSelection'

const file = (path: string, shared = false) => ({
  path,
  name: path.split('/').pop() ?? path,
  size: 10,
  mtime: '2026-10-01T00:00:00.000Z',
  shared,
})

const groups: DuplicateGroup[] = [
  { size: 10, hash: 'h1', reclaimable: 20, paths: [file('/a'), file('/b'), file('/s/c', true)] },
  { size: 100, hash: 'h2', reclaimable: 100, paths: [file('/x'), file('/y')] },
].map((g) => ({ ...g, paths: g.paths.map((f) => ({ ...f, size: g.size })) }))

describe('duplicate selection', () => {
  it('defaults to keeping paths[0] and removing every other copy', () => {
    const sel = initSelection(groups)
    expect(sel.h1).toEqual({ keep: '/a', remove: ['/b', '/s/c'] })
    expect(sel.h2).toEqual({ keep: '/x', remove: ['/y'] })
    const totals = dupTotals(groups, sel)
    expect(totals.count).toBe(3)
    expect(totals.bytes).toBe(2 * 10 + 100)
    expect(totals.shared).toEqual(['/s/c'])
    expect(totals.links).toEqual([
      { keep: '/a', paths: ['/b', '/s/c'] },
      { keep: '/x', paths: ['/y'] },
    ])
  })

  it('changing the kept copy flips the checkboxes', () => {
    let sel = initSelection(groups)
    sel = dupReducer(sel, { type: 'toggle', hash: 'h1', path: '/b', on: false })
    expect(sel.h1!.remove).toEqual(['/s/c'])
    sel = dupReducer(sel, { type: 'keep', hash: 'h1', path: '/s/c', group: groups[0]! })
    expect(sel.h1).toEqual({ keep: '/s/c', remove: ['/a', '/b'] })
    const totals = dupTotals(groups, sel)
    expect(totals.shared).toEqual([])
    expect(totals.links[0]).toEqual({ keep: '/s/c', paths: ['/a', '/b'] })
  })

  it('never marks the kept copy and supports select all / none', () => {
    let sel = initSelection(groups)
    sel = dupReducer(sel, { type: 'toggle', hash: 'h1', path: '/a', on: true })
    expect(sel.h1!.remove).not.toContain('/a')
    sel = dupReducer(sel, { type: 'clearAll' })
    expect(dupTotals(groups, sel).count).toBe(0)
    expect(sel.h1!.keep).toBe('/a')
    sel = dupReducer(sel, { type: 'keep', hash: 'h2', path: '/y', group: groups[1]! })
    sel = dupReducer(sel, { type: 'clearAll' })
    sel = dupReducer(sel, { type: 'selectAll', groups })
    expect(sel.h2).toEqual({ keep: '/y', remove: ['/x'] })
    expect(dupTotals(groups, sel).count).toBe(3)
  })

  it('keeps choices across a refreshed report and drops removed paths', () => {
    let sel = initSelection(groups)
    sel = dupReducer(sel, { type: 'keep', hash: 'h1', path: '/b', group: groups[0]! })
    const rep: DuplicateReport = {
      taskId: 't',
      path: '/',
      groups,
      reclaimable: 120,
      scanned: 5,
      candidates: 5,
      generatedAt: '2026-10-28T00:00:00.000Z',
      truncated: false,
      totalGroups: 2,
    }
    const pruned = withoutPaths(rep, ['/s/c', '/y'])
    expect(pruned.groups.map((g) => g.hash)).toEqual(['h1'])
    expect(pruned.reclaimable).toBe(10)
    sel = dupReducer(sel, { type: 'init', groups: pruned.groups })
    expect(sel).toEqual({ h1: { keep: '/b', remove: ['/a'] } })
  })
})

describe('storage tab deep link', () => {
  it('accepts only ?tab=duplicates', () => {
    expect(parseStorageSearch({ tab: 'duplicates' })).toEqual({ tab: 'duplicates' })
    expect(parseStorageSearch({ tab: 'summary' })).toEqual({})
    expect(parseStorageSearch({ tab: 'x', other: 1 })).toEqual({})
    expect(parseStorageSearch({})).toEqual({})
  })
})
