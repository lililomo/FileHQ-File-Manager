import { describe, expect, it } from 'vitest'
import type { Item } from '@/features/browser/api'
import { actionById, applicableActions, isEnabled, menuItems, type ActionContext } from './actions'

const item = (path: string, isDir = false) => ({ path, name: path.split('/').pop(), isDir }) as Item

const DUAL_KEYS = new Set<string | symbol>([
  'otherPanePath',
  'copyToOtherPane',
  'moveToOtherPane',
  'toggleDualPane',
])

function ctx(over: Partial<ActionContext>): ActionContext {
  const noop = () => {}
  return new Proxy(
    {
      path: '/a',
      selection: [],
      selectionCount: 0,
      total: 3,
      clipboardCount: 0,
      canUndo: false,
      ...over,
    } as ActionContext,
    {
      // dual-pane fields stay undefined unless given
      get: (t, k) => (k in t ? t[k as keyof ActionContext] : DUAL_KEYS.has(k) ? undefined : noop),
    },
  )
}

describe('"… ke panel lain" actions (Step 23b)', () => {
  const copy = actionById('copyToOtherPane')!
  const move = actionById('moveToOtherPane')!
  it('exist only while dual-pane is shown', () => {
    const single = ctx({ selection: [item('/a/x')], selectionCount: 1 })
    expect(copy.isApplicable(single)).toBe(false)
    expect(
      menuItems('file', single)
        .flat()
        .map((a) => a.id),
    ).not.toContain('copyToOtherPane')
    const dual = ctx({
      selection: [item('/a/x')],
      selectionCount: 1,
      otherPanePath: '/b',
      copyToOtherPane: () => {},
      moveToOtherPane: () => {},
    })
    const ids = menuItems('file', dual)
      .flat()
      .map((a) => a.id)
    expect(ids).toContain('copyToOtherPane')
    expect(ids).toContain('moveToOtherPane')
    expect(isEnabled(copy, dual)).toBe(true)
    expect(isEnabled(move, dual)).toBe(true)
  })
  it('are disabled for an empty selection, the same folder or a folder into itself', () => {
    const base = { otherPanePath: '/b', copyToOtherPane: () => {}, moveToOtherPane: () => {} }
    expect(isEnabled(copy, ctx({ ...base }))).toBe(false)
    expect(
      isEnabled(
        move,
        ctx({ ...base, otherPanePath: '/a', selection: [item('/a/x')], selectionCount: 1 }),
      ),
    ).toBe(false)
    expect(
      isEnabled(
        move,
        ctx({
          ...base,
          otherPanePath: '/a/d/e',
          selection: [item('/a/d', true)],
          selectionCount: 1,
        }),
      ),
    ).toBe(false)
    // the palette lists only runnable actions
    expect(applicableActions(ctx({ ...base })).map((a) => a.id)).not.toContain('copyToOtherPane')
  })
})
