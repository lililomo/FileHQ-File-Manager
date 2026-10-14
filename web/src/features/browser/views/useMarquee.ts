import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from 'react'
import { normalizeRect, type Rect } from './gridMath'

export interface MarqueeOptions {
  enabled: boolean
  /** True when the pointer went down on empty background (not on a cell). */
  isBackground: (target: EventTarget | null) => boolean
  /** Cells intersecting a rectangle in content coordinates. */
  toIndices: (rect: Rect) => number[]
  /** Called with the intersecting indices while dragging; additive = Ctrl/Shift held at start. */
  onSelect: (indices: number[], additive: boolean) => void
  /** A plain click on the background (no drag). */
  onBackgroundClick?: (additive: boolean) => void
}

const DRAG_THRESHOLD = 4

/** Marquee (rubber-band) selection for grid views (SPEC §9.5). The rectangle is
 *  kept in content coordinates so scrolling during the drag keeps working. */
export function useMarquee(opts: MarqueeOptions) {
  const [rect, setRect] = useState<Rect | null>(null)
  const drag = useRef<{
    x: number
    y: number
    additive: boolean
    el: HTMLElement
    moved: boolean
  } | null>(null)
  const raf = useRef(0)
  const latest = useRef(opts)
  useEffect(() => {
    latest.current = opts
  })

  const contentPoint = (el: HTMLElement, e: { clientX: number; clientY: number }) => {
    const b = el.getBoundingClientRect()
    return { x: e.clientX - b.left + el.scrollLeft, y: e.clientY - b.top + el.scrollTop }
  }

  const onPointerDown = useCallback((e: ReactPointerEvent<HTMLElement>) => {
    const o = latest.current
    if (!o.enabled || e.button !== 0 || !o.isBackground(e.target)) return
    const el = e.currentTarget
    const p = contentPoint(el, e)
    drag.current = {
      x: p.x,
      y: p.y,
      additive: e.ctrlKey || e.metaKey || e.shiftKey,
      el,
      moved: false,
    }
    el.setPointerCapture(e.pointerId)
  }, [])

  useEffect(() => {
    const move = (e: PointerEvent) => {
      const d = drag.current
      if (!d) return
      const p = contentPoint(d.el, e)
      const r = normalizeRect(d.x, d.y, p.x, p.y)
      if (!d.moved && r.w < DRAG_THRESHOLD && r.h < DRAG_THRESHOLD) return
      d.moved = true
      e.preventDefault()
      cancelAnimationFrame(raf.current)
      raf.current = requestAnimationFrame(() => {
        setRect(r)
        latest.current.onSelect(latest.current.toIndices(r), d.additive)
      })
      // auto-scroll near the edges
      const b = d.el.getBoundingClientRect()
      if (e.clientY > b.bottom - 24) d.el.scrollTop += 12
      else if (e.clientY < b.top + 24) d.el.scrollTop -= 12
    }
    const up = () => {
      const d = drag.current
      if (!d) return
      drag.current = null
      cancelAnimationFrame(raf.current)
      setRect(null)
      if (!d.moved) latest.current.onBackgroundClick?.(d.additive)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', up)
    return () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', up)
      cancelAnimationFrame(raf.current)
    }
  }, [])

  return { rect, onPointerDown }
}
