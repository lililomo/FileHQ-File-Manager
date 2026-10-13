import { describe, expect, it } from 'vitest'
import {
  EMPTY_SELECTION,
  click,
  headerState,
  invert,
  isSelected,
  prune,
  selectAll,
  selectRange,
  selectedCount,
  selectedItems,
  toggle,
} from './SelectionModel'

const items = ['/a', '/b', '/c', '/d', '/e'].map((path, i) => ({ path, size: i + 1, isDir: false }))

describe('SelectionModel', () => {
  it('plain click selects only and sets the anchor', () => {
    const s = click(EMPTY_SELECTION, items, '/b', {})
    expect([...s.paths]).toEqual(['/b'])
    expect(s.anchor).toBe('/b')
    const s2 = click(s, items, '/d', {})
    expect([...s2.paths]).toEqual(['/d'])
  })
  it('ctrl toggles without moving other selections', () => {
    let s = click(EMPTY_SELECTION, items, '/b', {})
    s = click(s, items, '/d', { ctrl: true })
    expect([...s.paths].sort()).toEqual(['/b', '/d'])
    s = click(s, items, '/b', { ctrl: true })
    expect([...s.paths]).toEqual(['/d'])
  })
  it('shift selects the range from the anchor in either direction', () => {
    let s = click(EMPTY_SELECTION, items, '/d', {})
    s = click(s, items, '/b', { shift: true })
    expect([...s.paths].sort()).toEqual(['/b', '/c', '/d'])
    expect(s.anchor).toBe('/d')
    s = click(s, items, '/e', { shift: true })
    expect([...s.paths].sort()).toEqual(['/d', '/e'])
    // ctrl+shift adds the range to the existing selection
    s = click(s, items, '/a', { ctrl: true })
    s = click(s, items, '/b', { ctrl: true, shift: true })
    expect([...s.paths].sort()).toEqual(['/a', '/b', '/d', '/e'])
    expect(selectRange(EMPTY_SELECTION, items, '/zzz').paths.size).toBe(0)
  })
  it('select all is virtual and counts the folder total', () => {
    const s = selectAll()
    expect(selectedCount(s, items.length, 10_000)).toBe(10_000)
    expect(isSelected(s, '/nope')).toBe(true)
    const t = toggle(s, '/b')
    expect(selectedCount(t, items.length, 10_000)).toBe(9_999)
    expect(isSelected(t, '/b')).toBe(false)
    expect(selectedItems(t, items).map((i) => i.path)).toEqual(['/a', '/c', '/d', '/e'])
    expect(headerState(s, items.length, 10_000)).toBe('all')
    expect(headerState(t, items.length, 10_000)).toBe('some')
    expect(headerState(EMPTY_SELECTION, items.length, 5)).toBe('none')
  })
  it('invert flips loaded items and round-trips "all"', () => {
    let s = click(EMPTY_SELECTION, items, '/a', {})
    s = invert(s, items)
    expect([...s.paths].sort()).toEqual(['/b', '/c', '/d', '/e'])
    expect(invert(EMPTY_SELECTION, items).mode).toBe('all')
    const t = invert(toggle(selectAll(), '/c'), items)
    expect(t.mode).toBe('explicit')
    expect([...t.paths]).toEqual(['/c'])
  })
  it('prunes vanished paths and keeps the anchor when it survives', () => {
    let s = click(EMPTY_SELECTION, items, '/a', {})
    s = click(s, items, '/b', { ctrl: true })
    const pruned = prune(
      s,
      items.filter((i) => i.path !== '/a'),
    )
    expect([...pruned.paths]).toEqual(['/b'])
    expect(pruned.anchor).toBe('/b')
    expect(prune(s, items)).toBe(s)
  })
})
