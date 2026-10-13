import { beforeEach, describe, expect, it } from 'vitest'
import { useTabsStore } from './tabs'

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
