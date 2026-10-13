import { beforeEach, describe, expect, it } from 'vitest'
import { MAX_TABS, useTabsStore } from './tabs'

describe('tabs store history', () => {
  beforeEach(() => {
    useTabsStore.setState({
      tabs: [{ id: 'tab-1', path: '/', history: ['/'], index: 0, scroll: {} }],
      activeId: 'tab-1',
      pending: null,
    })
  })
  it('pushes visits and supports back/forward without duplicating entries', () => {
    const s = useTabsStore.getState()
    s.visit('/a')
    s.visit('/a/b')
    expect(useTabsStore.getState().active().history).toEqual(['/', '/a', '/a/b'])
    expect(s.canBack()).toBe(true)
    expect(s.canForward()).toBe(false)
    const target = s.back()
    expect(target).toBe('/a')
    s.visit('/a') // router confirms the navigation
    const t = useTabsStore.getState().active()
    expect(t.index).toBe(1)
    expect(t.history).toEqual(['/', '/a', '/a/b'])
    expect(s.canForward()).toBe(true)
    expect(s.forward()).toBe('/a/b')
    s.visit('/a/b')
    expect(useTabsStore.getState().active().index).toBe(2)
  })
  it('truncates forward history on a new visit', () => {
    const s = useTabsStore.getState()
    s.visit('/a')
    s.visit('/b')
    s.visit(s.back()!)
    s.visit('/c')
    expect(useTabsStore.getState().active().history).toEqual(['/', '/a', '/c'])
  })
  it('ignores a revisit of the current entry', () => {
    const s = useTabsStore.getState()
    s.visit('/a')
    s.visit('/a')
    expect(useTabsStore.getState().active().history).toEqual(['/', '/a'])
  })
})

describe('tabs store multi-tab', () => {
  beforeEach(() => {
    useTabsStore.setState({
      tabs: [{ id: 'tab-1', path: '/', history: ['/'], index: 0, scroll: {} }],
      activeId: 'tab-1',
      pending: null,
    })
  })
  it('opens next to the active tab, caps at MAX_TABS, keeps histories separate', () => {
    const s = useTabsStore.getState()
    const t = s.openTab('/a')!
    expect(useTabsStore.getState().activeId).toBe(t.id)
    s.visit('/a/x')
    expect(useTabsStore.getState().active().history).toEqual(['/a', '/a/x'])
    expect(useTabsStore.getState().tabs[0]!.history).toEqual(['/'])
    for (let i = useTabsStore.getState().tabs.length; i < MAX_TABS; i++) {
      expect(s.openTab(`/t${i}`, { background: true })).not.toBeNull()
    }
    expect(useTabsStore.getState().tabs).toHaveLength(MAX_TABS)
    expect(s.openTab('/overflow')).toBeNull()
  })
  it('close/closeOthers/closeRight/activate/reorder', () => {
    const s = useTabsStore.getState()
    const a = s.openTab('/a')!
    const b = s.openTab('/b')!
    const c = s.openTab('/c')!
    expect(useTabsStore.getState().tabs.map((t) => t.path)).toEqual(['/', '/a', '/b', '/c'])
    // closing the active tab activates the right neighbour, else the left one
    expect(s.activate(b.id)).toBe('/b')
    expect(s.closeTab(b.id)).toBe('/c')
    expect(s.closeTab(c.id)).toBe('/a')
    expect(useTabsStore.getState().activeId).toBe(a.id)
    s.openTab('/d')
    s.openTab('/e')
    s.closeRight(a.id)
    expect(useTabsStore.getState().tabs.map((t) => t.path)).toEqual(['/', '/a'])
    expect(useTabsStore.getState().activeId).toBe(a.id)
    s.openTab('/f')
    s.closeOthers(a.id)
    expect(useTabsStore.getState().tabs.map((t) => t.path)).toEqual(['/a'])
    // closing the last tab leaves one fresh root tab
    expect(s.closeTab(a.id)).toBe('/')
    expect(useTabsStore.getState().tabs).toHaveLength(1)
    s.openTab('/x')
    s.openTab('/y')
    const yId = useTabsStore.getState().activeId
    s.reorder(yId, 0)
    expect(useTabsStore.getState().tabs.map((t) => t.path)).toEqual(['/y', '/', '/x'])
  })
  it('hydrates persisted tabs and guarantees a tab for the current path', () => {
    const s = useTabsStore.getState()
    s.hydrate(['/a', '/b', '/a', 'relative'], '/b')
    expect(useTabsStore.getState().tabs.map((t) => t.path)).toEqual(['/a', '/b', '/a'])
    expect(useTabsStore.getState().active().path).toBe('/b')
    // unknown current path → continues in the first restored tab, Back reaches its old path
    s.hydrate(['/a', '/b'], '/zzz')
    expect(useTabsStore.getState().tabs.map((t) => t.path)).toEqual(['/zzz', '/b'])
    expect(useTabsStore.getState().active().history).toEqual(['/a', '/zzz'])
    expect(s.canBack()).toBe(true)
    s.hydrate([], '/solo')
    expect(useTabsStore.getState().tabs.map((t) => t.path)).toEqual(['/solo'])
    const many = Array.from({ length: 12 }, (_, i) => `/p${i}`)
    s.hydrate(many, '/current')
    expect(useTabsStore.getState().tabs).toHaveLength(MAX_TABS)
    expect(useTabsStore.getState().active().path).toBe('/current')
    expect(useTabsStore.getState().tabs[0]!.path).toBe('/current')
  })
})
