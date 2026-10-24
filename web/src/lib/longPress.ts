import type {
  MouseEvent as ReactMouseEvent,
  PointerEvent as ReactPointerEvent,
  TouchEvent as ReactTouchEvent,
} from 'react'

/** SPEC §9.9: a 500 ms touch long-press opens the context menu. */
export const LONG_PRESS_MS = 500
const MOVE_TOLERANCE = 10

interface Press {
  timer: number
  x: number
  y: number
  fired: boolean
}

/**
 * Handlers for a container: a touch (or pen) held still for LONG_PRESS_MS
 * dispatches a `contextmenu` event at the finger on the pressed element, so
 * Radix ContextMenu (whose own touch delay is a fixed 700 ms) opens the same
 * menu as a right click. The compatibility mouse events that end the press
 * are cancelled (`touchend` preventDefault): their mousedown would move focus
 * out of the freshly opened menu and close it, and the click must not
 * select/open the item underneath.
 */
export function createLongPress(delay = LONG_PRESS_MS) {
  let press: Press | null = null
  let swallowClick = false
  const cancel = () => {
    if (press) window.clearTimeout(press.timer)
    press = null
  }
  return {
    onPointerDown(e: ReactPointerEvent) {
      if (e.pointerType === 'mouse' || !e.isPrimary) return
      cancel()
      swallowClick = false
      const target = e.target as Element
      const { clientX, clientY } = e
      press = {
        x: clientX,
        y: clientY,
        fired: false,
        timer: window.setTimeout(() => {
          if (!press) return
          press.fired = true
          swallowClick = true
          target.dispatchEvent(
            new MouseEvent('contextmenu', {
              bubbles: true,
              cancelable: true,
              clientX,
              clientY,
              button: 2,
            }),
          )
        }, delay),
      }
    },
    onPointerMove(e: ReactPointerEvent) {
      if (!press) return
      if (Math.hypot(e.clientX - press.x, e.clientY - press.y) > MOVE_TOLERANCE) cancel()
    },
    onPointerUp: cancel,
    onPointerCancel: cancel,
    onTouchEnd(e: ReactTouchEvent) {
      if (swallowClick && e.cancelable) e.preventDefault()
    },
    onClickCapture(e: ReactMouseEvent) {
      if (!swallowClick) return
      swallowClick = false
      e.preventDefault()
      e.stopPropagation()
    },
  }
}
