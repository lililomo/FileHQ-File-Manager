import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MouseEvent, PointerEvent, TouchEvent } from 'react'
import { createLongPress, LONG_PRESS_MS } from './longPress'

function pointer(target: Element, type: string, x = 10, y = 10) {
  return {
    target,
    pointerType: type,
    isPrimary: true,
    clientX: x,
    clientY: y,
  } as unknown as PointerEvent
}

describe('createLongPress (SPEC §9.9)', () => {
  let el: HTMLDivElement
  let menus: number
  beforeEach(() => {
    vi.useFakeTimers()
    el = document.createElement('div')
    document.body.appendChild(el)
    menus = 0
    el.addEventListener('contextmenu', () => menus++)
  })
  afterEach(() => {
    vi.useRealTimers()
    el.remove()
  })

  it('opens the context menu after 500 ms of a still touch and eats the closing click', () => {
    const lp = createLongPress()
    lp.onPointerDown(pointer(el, 'touch'))
    vi.advanceTimersByTime(LONG_PRESS_MS - 1)
    expect(menus).toBe(0)
    vi.advanceTimersByTime(1)
    expect(menus).toBe(1)
    lp.onPointerUp()
    const end = { cancelable: true, preventDefault: vi.fn() } as unknown as TouchEvent
    lp.onTouchEnd(end)
    expect(end.preventDefault).toHaveBeenCalled()
    const click = { preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as MouseEvent
    lp.onClickCapture(click)
    expect(click.stopPropagation).toHaveBeenCalled()
    // the next tap is a normal tap again
    lp.onPointerDown(pointer(el, 'touch'))
    lp.onPointerUp()
    const click2 = { preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as MouseEvent
    lp.onClickCapture(click2)
    expect(click2.stopPropagation).not.toHaveBeenCalled()
  })

  it('ignores mouse, short taps and moving fingers', () => {
    const lp = createLongPress()
    lp.onPointerDown(pointer(el, 'mouse'))
    vi.advanceTimersByTime(1000)
    lp.onPointerDown(pointer(el, 'touch'))
    vi.advanceTimersByTime(200)
    lp.onPointerUp()
    vi.advanceTimersByTime(1000)
    lp.onPointerDown(pointer(el, 'touch', 10, 10))
    lp.onPointerMove(pointer(el, 'touch', 10, 40))
    vi.advanceTimersByTime(1000)
    expect(menus).toBe(0)
  })
})
