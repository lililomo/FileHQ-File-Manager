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
    expect(hydrateRightPane({ tabs: [{ path: '/x' }, { path: '/y' }], active: 1 })).toBe(true)
    const s = useRightTabsStore.getState()
    expect(s.tabs.map((x) => x.path)).toEqual(['/x', '/y'])
    expect(s.active().path).toBe('/y')
    // a remount of the browser must not overwrite the live state
    useRightTabsStore.getState().visit('/y/z')
    expect(hydrateRightPane({ tabs: [{ path: '/x' }] })).toBe(false)
    expect(useRightTabsStore.getState().active().path).toBe('/y/z')
  })

  it('starts at the root when nothing (or garbage) is stored', () => {
    expect(hydrateRightPane({ tabs: 'nope' })).toBe(false)
    expect(useRightTabsStore.getState().active().path).toBe('/')
  })
})
