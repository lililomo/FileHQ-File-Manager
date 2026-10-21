import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import {
  INITIAL_ZOOM,
  reduceZoom,
  type Box,
  type Point,
  type ZoomAction,
  type ZoomState,
} from './zoomPan'

/** Wheel sensitivity: one notch (±100) ≈ ×1.16. */
const WHEEL_FACTOR = 0.0015
/** Pointer distance before a press becomes a drag (lets clicks through). */
const DRAG_THRESHOLD = 3

export interface ZoomPanBind {
  onPointerDown: (e: React.PointerEvent<HTMLElement>) => void
  onPointerMove: (e: React.PointerEvent<HTMLElement>) => void
  onPointerUp: (e: React.PointerEvent<HTMLElement>) => void
  onPointerCancel: (e: React.PointerEvent<HTMLElement>) => void
  onDoubleClick: (e: React.MouseEvent<HTMLElement>) => void
}

/** Zoom/pan state bound to a viewport element: wheel zooms around the
 *  cursor (native non-passive listener, React's onWheel is passive), one
 *  pointer drags, two pointers pinch, double-click toggles fit ↔ 1:1, and a
 *  ResizeObserver keeps fit/fill modes aligned with the viewport. */
export function useZoomPan(natural: Box | null) {
  const viewportRef = useRef<HTMLDivElement | null>(null)
  const [viewport, setViewport] = useState<Box>({ w: 0, h: 0 })
  const [state, setState] = useState<ZoomState>(INITIAL_ZOOM)
  const [dragging, setDragging] = useState(false)
  // latest boxes for event-time reducers (written after commit, never in render)
  const boxes = useRef<{ viewport: Box; natural: Box }>({
    viewport,
    natural: natural ?? { w: 0, h: 0 },
  })
  const nw = natural?.w ?? 0
  const nh = natural?.h ?? 0
  useLayoutEffect(() => {
    boxes.current = { viewport, natural: { w: nw, h: nh } }
  }, [viewport, nw, nh])

  const dispatch = useCallback((action: ZoomAction) => {
    setState((s) => reduceZoom(s, action, boxes.current.viewport, boxes.current.natural))
  }, [])

  // viewport size
  useEffect(() => {
    const el = viewportRef.current
    if (!el) return
    const measure = () => setViewport({ w: el.clientWidth, h: el.clientHeight })
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  // re-apply fit/fill when the viewport or the image changes
  useEffect(() => {
    if (nw > 0 && nh > 0 && viewport.w > 0) dispatch({ type: 'layout' })
  }, [nw, nh, viewport.w, viewport.h, dispatch])

  const pointAt = useCallback((clientX: number, clientY: number): Point => {
    const el = viewportRef.current
    if (!el) return { x: 0, y: 0 }
    const r = el.getBoundingClientRect()
    return { x: clientX - r.left - r.width / 2, y: clientY - r.top - r.height / 2 }
  }, [])

  // wheel: non-passive so the page does not scroll
  useEffect(() => {
    const el = viewportRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const delta = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY
      const factor = Math.min(2, Math.max(0.5, Math.exp(-delta * WHEEL_FACTOR)))
      dispatch({ type: 'zoomBy', factor, at: pointAt(e.clientX, e.clientY) })
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [dispatch, pointAt])

  // pointers: drag (1) and pinch (2)
  const pointers = useRef(new Map<number, Point>())
  const press = useRef<{ x: number; y: number; moved: boolean } | null>(null)
  const pinch = useRef<number | null>(null)
  const bind = useMemo<ZoomPanBind>(
    () => ({
      onPointerDown: (e) => {
        if (e.button !== 0 && e.pointerType === 'mouse') return
        e.currentTarget.setPointerCapture(e.pointerId)
        pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
        if (pointers.current.size === 1)
          press.current = { x: e.clientX, y: e.clientY, moved: false }
        else if (pointers.current.size === 2) {
          const [a, b] = [...pointers.current.values()]
          pinch.current = Math.hypot(a!.x - b!.x, a!.y - b!.y)
        }
      },
      onPointerMove: (e) => {
        const prev = pointers.current.get(e.pointerId)
        if (!prev) return
        const cur = { x: e.clientX, y: e.clientY }
        pointers.current.set(e.pointerId, cur)
        if (pointers.current.size === 2 && pinch.current) {
          const [a, b] = [...pointers.current.values()]
          const d = Math.hypot(a!.x - b!.x, a!.y - b!.y)
          if (d > 0) {
            const mid = pointAt((a!.x + b!.x) / 2, (a!.y + b!.y) / 2)
            dispatch({ type: 'zoomBy', factor: d / pinch.current, at: mid })
            pinch.current = d
          }
          return
        }
        if (pointers.current.size !== 1 || !press.current) return
        const dx = cur.x - prev.x
        const dy = cur.y - prev.y
        if (!press.current.moved) {
          if (Math.hypot(cur.x - press.current.x, cur.y - press.current.y) < DRAG_THRESHOLD) return
          press.current.moved = true
          setDragging(true)
        }
        dispatch({ type: 'pan', dx, dy })
      },
      onPointerUp: (e) => {
        pointers.current.delete(e.pointerId)
        if (e.currentTarget.hasPointerCapture(e.pointerId))
          e.currentTarget.releasePointerCapture(e.pointerId)
        if (pointers.current.size < 2) pinch.current = null
        if (pointers.current.size === 0) {
          press.current = null
          setDragging(false)
        }
      },
      onPointerCancel: (e) => {
        pointers.current.delete(e.pointerId)
        pinch.current = null
        press.current = null
        setDragging(false)
      },
      onDoubleClick: (e) => {
        const at = pointAt(e.clientX, e.clientY)
        setState((s) =>
          reduceZoom(
            s,
            s.mode === 'fit' ? { type: 'zoomTo', scale: 1, at } : { type: 'fit' },
            boxes.current.viewport,
            boxes.current.natural,
          ),
        )
      },
    }),
    [dispatch, pointAt],
  )

  return { viewportRef, viewport, state, dispatch, bind, dragging }
}
