import { useCallback, useEffect, useRef } from 'react'
import type { Item } from '../api'

export const SLOW_CLICK_MS = 600

/** Explorer-style "slow click" rename: a plain click on the *name* of the
 *  item that is already the only selected one starts an inline rename after a
 *  short delay, unless a double-click follows. */
export function useSlowClick(onSlowClick?: ((item: Item) => void) | undefined) {
  const timer = useRef(0)
  const cancel = useCallback(() => {
    if (timer.current) {
      clearTimeout(timer.current)
      timer.current = 0
    }
  }, [])
  useEffect(() => cancel, [cancel])
  const onClick = useCallback(
    (e: React.MouseEvent, item: Item, wasOnlySelected: boolean) => {
      cancel()
      if (!onSlowClick || !wasOnlySelected || e.ctrlKey || e.metaKey || e.shiftKey || e.detail > 1)
        return
      if (!(e.target instanceof HTMLElement && e.target.closest('[data-name]'))) return
      timer.current = window.setTimeout(() => {
        timer.current = 0
        onSlowClick(item)
      }, SLOW_CLICK_MS)
    },
    [onSlowClick, cancel],
  )
  return { onClick, cancel }
}
