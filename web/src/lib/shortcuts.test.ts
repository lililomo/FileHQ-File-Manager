import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  FORBIDDEN_SHORTCUTS,
  SHORTCUTS,
  findConflicts,
  formatKeys,
  matches,
  parseKeys,
  pushScope,
  registerShortcut,
  resetShortcuts,
} from './shortcuts'

function kb(
  init: Partial<KeyboardEventInit> & { key: string },
  target?: HTMLElement,
): KeyboardEvent {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })
  if (target) Object.defineProperty(e, 'target', { value: target })
  return e
}

afterEach(() => resetShortcuts())

describe('shortcut registry', () => {
  it('parses key strings', () => {
    expect(parseKeys('Ctrl+Shift+F')).toEqual({ key: 'F', ctrl: true, shift: true, alt: false })
    expect(parseKeys('Alt+ArrowLeft')).toEqual({
      key: 'ArrowLeft',
      ctrl: false,
      shift: false,
      alt: true,
    })
    expect(parseKeys('Space').key).toBe(' ')
    expect(parseKeys('?').key).toBe('?')
    expect(parseKeys('F2').key).toBe('F2')
  })
  it('matches Ctrl and Meta alike, ignores Shift for ?', () => {
    const c = parseKeys('Ctrl+K')
    expect(matches(kb({ key: 'k', ctrlKey: true }), c)).toBe(true)
    expect(matches(kb({ key: 'k', metaKey: true }), c)).toBe(true)
    expect(matches(kb({ key: 'k' }), c)).toBe(false)
    expect(matches(kb({ key: 'k', ctrlKey: true, shiftKey: true }), c)).toBe(false)
    expect(matches(kb({ key: '?', shiftKey: true }), parseKeys('?'))).toBe(true)
  })
  it('never uses a forbidden browser combination', () => {
    const forbidden = FORBIDDEN_SHORTCUTS.map((k) => JSON.stringify(parseKeys(k)))
    for (const s of SHORTCUTS) {
      for (const k of [s.keys, ...(s.alt ?? [])]) {
        expect(forbidden, `${s.id} uses ${k}`).not.toContain(JSON.stringify(parseKeys(k)))
      }
    }
  })
  it('has no conflicting combinations within a scope', () => {
    expect(findConflicts()).toEqual([])
    expect(
      findConflicts([
        { id: 'a', keys: 'Ctrl+K', scope: 'browser', labelKey: 'a' },
        { id: 'b', keys: 'Ctrl+K', scope: 'browser', labelKey: 'b' },
      ]),
    ).toHaveLength(1)
  })
  it('dispatches by scope and skips editable targets', () => {
    const refresh = vi.fn()
    const palette = vi.fn()
    registerShortcut('refresh', refresh)
    registerShortcut('palette', palette)
    // browser scope not active yet → refresh ignored, global palette works
    window.dispatchEvent(kb({ key: 'F5' }))
    expect(refresh).not.toHaveBeenCalled()
    window.dispatchEvent(kb({ key: 'k', ctrlKey: true }))
    expect(palette).toHaveBeenCalledTimes(1)
    const pop = pushScope('browser')
    window.dispatchEvent(kb({ key: 'F5' }))
    expect(refresh).toHaveBeenCalledTimes(1)
    // typing in an input: F5 ignored, Ctrl+K (allowInInput) still works
    const input = document.createElement('input')
    document.body.append(input)
    input.dispatchEvent(kb({ key: 'F5' }, input))
    expect(refresh).toHaveBeenCalledTimes(1)
    input.dispatchEvent(kb({ key: 'k', ctrlKey: true }, input))
    expect(palette).toHaveBeenCalledTimes(2)
    // dialog scope swallows browser shortcuts
    const popDialog = pushScope('dialog')
    window.dispatchEvent(kb({ key: 'F5' }))
    expect(refresh).toHaveBeenCalledTimes(1)
    popDialog()
    window.dispatchEvent(kb({ key: 'F5' }))
    expect(refresh).toHaveBeenCalledTimes(2)
    pop()
    input.remove()
  })
  it('formats keys for display', () => {
    expect(formatKeys('Alt+ArrowLeft')).toEqual(['Alt', '←'])
    expect(formatKeys('Space')).toEqual(['Space'])
  })
})
