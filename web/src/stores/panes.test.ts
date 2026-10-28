import { beforeEach, describe, expect, it } from 'vitest'
import {
  hydrateRightPane,
  isDualPaneActive,
  isRightPaneHydrated,
  otherPane,
  parseRightPref,
  resetPanes,
  rightPrefOf,
  usePanesStore,
  useRightTabsStore,
} from './panes'
import { MAX_TABS, useTabsStore } from './tabs'

describe('pane store (Step 23b)', () => {
  beforeEach(() => {
    resetPanes()
    useTabsStore.getState().restore(['/'], 0)
  })

  it('has exactly one focused pane, left by default', () => {
    expect(usePanesStore.getState().focused).toBe('left')
    usePanesStore.getState().focus('right')
    expect(usePanesStore.getState().focused).toBe('right')
    expect(otherPane('right')).toBe('left')
    expect(otherPane('left')).toBe('right')
  })

  it('falls back to a single pane below 1280 px and on phones', () => {
    expect(isDualPaneActive(true, true)).toBe(true)
    expect(isDualPaneActive(true, false)).toBe(false)
    expect(isDualPaneActive(true, true, true)).toBe(false)
    expect(isDualPaneActive(false, true)).toBe(false)
  })

  it('keeps the right pane tabs separate from the left pane (≤ 10, own ids and history)', () => {
    const right = useRightTabsStore.getState()
    right.visit('/a')
    right.visit('/a/b')
    expect(useRightTabsStore.getState().active().history).toEqual(['/', '/a', '/a/b'])
    expect(useTabsStore.getState().active().path).toBe('/')
    expect(useRightTabsStore.getState().activeId).toMatch(/^rtab-/)
    expect(useTabsStore.getState().activeId).toMatch(/^tab-/)
    // back/forward without a router: the pane visits the target itself
    const back = right.back()
    expect(back).toBe('/a')
    right.visit(back!)
    expect(useRightTabsStore.getState().active().path).toBe('/a')
    expect(useRightTabsStore.getState().canForward()).toBe(true)
    for (let i = useRightTabsStore.getState().tabs.length; i < MAX_TABS; i++)
      expect(right.openTab(`/t${i}`)).not.toBeNull()
    expect(right.openTab('/eleven')).toBeNull()
    expect(useRightTabsStore.getState().tabs).toHaveLength(MAX_TABS)
    expect(useTabsStore.getState().tabs).toHaveLength(1)
  })

  it('keeps the sort override per right pane only', () => {
    usePanesStore.getState().setRightSort({ field: 'size', dir: 'desc' })
    expect(usePanesStore.getState().rightSort).toEqual({ field: 'size', dir: 'desc' })
  })

  it('persists the right pane as {tabs:[{path}], active}', () => {
    const right = useRightTabsStore.getState()
    right.visit('/docs')
    right.openTab('/foto')
    expect(rightPrefOf(useRightTabsStore.getState())).toEqual({
      tabs: [{ path: '/docs' }, { path: '/foto' }],
      active: 1,
    })
  })

  it('parses stored values defensively', () => {
    expect(parseRightPref(undefined)).toBeNull()
    expect(parseRightPref([{ path: '/' }])).toBeNull()
    expect(parseRightPref({ tabs: [] })).toBeNull()
    expect(parseRightPref({ tabs: [{ path: 'rel' }, { path: '/ok' }], active: 5 })).toEqual({
      tabs: [{ path: '/ok' }],
      active: 0,
    })
    const many = { tabs: Array.from({ length: 12 }, (_, i) => ({ path: `/${i}` })), active: 3 }
    expect(parseRightPref(many)?.tabs).toHaveLength(MAX_TABS)
    expect(parseRightPref(many)?.active).toBe(3)
  })

  it('hydrates the right pane once per page load', () => {
    expect(isRightPaneHydrated()).toBe(false)
    expect(hydrateRightPane({ tabs: [{ path: '/x' }, { path: '/y' }], active: 1 }, 'u1')).toBe(true)
    const s = useRightTabsStore.getState()
    expect(s.tabs.map((x) => x.path)).toEqual(['/x', '/y'])
    expect(s.active().path).toBe('/y')
    // a remount of the browser must not overwrite the live state
    useRightTabsStore.getState().visit('/y/z')
    expect(hydrateRightPane({ tabs: [{ path: '/x' }] }, 'u1')).toBe(false)
    expect(useRightTabsStore.getState().active().path).toBe('/y/z')
  })

  it('starts at the root when nothing (or garbage) is stored', () => {
    expect(hydrateRightPane({ tabs: 'nope' }, 'u1')).toBe(false)
    expect(useRightTabsStore.getState().active().path).toBe('/')
  })

  it('does not carry one user’s right pane over to the next login without a reload', () => {
    expect(
      hydrateRightPane({ tabs: [{ path: '/rahasia' }, { path: '/y' }], active: 1 }, 'u1'),
    ).toBe(true)
    usePanesStore.getState().focus('right')
    usePanesStore.getState().setRightSort({ field: 'size', dir: 'desc' })
    // user 2 with a stored value: their tabs replace user 1's, focus and sort reset
    expect(hydrateRightPane({ tabs: [{ path: '/z' }] }, 'u2')).toBe(true)
    expect(useRightTabsStore.getState().tabs.map((x) => x.path)).toEqual(['/z'])
    expect(usePanesStore.getState().focused).toBe('left')
    expect(usePanesStore.getState().rightSort).toBeNull()
    // user 3 without a stored value: back to the root, nothing of user 2 remains
    expect(hydrateRightPane(undefined, 'u3')).toBe(false)
    expect(useRightTabsStore.getState().tabs.map((x) => x.path)).toEqual(['/'])
    // the same user again (remount) keeps the live state
    useRightTabsStore.getState().visit('/live')
    expect(hydrateRightPane(undefined, 'u3')).toBe(false)
    expect(useRightTabsStore.getState().active().path).toBe('/live')
  })
})
