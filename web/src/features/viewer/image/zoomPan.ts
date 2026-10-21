/** Pure zoom/pan/rotate/flip state of the image viewer (SPEC §8.14
 *  ImageViewer). Rendering is one CSS transform on a centred <img>:
 *  `translate(x, y) scale(scale) rotate(rotate) scale(flipH, flipV)`.
 *  Everything here is unit-tested without the DOM. */

export interface Box {
  w: number
  h: number
}

export interface Point {
  x: number
  y: number
}

export type Rotation = 0 | 90 | 180 | 270

export interface ZoomState {
  scale: number
  /** pan offset of the image centre from the viewport centre (px) */
  x: number
  y: number
  rotate: Rotation
  flipH: boolean
  flipV: boolean
  /** fit/fill follow the viewport on resize; custom keeps the chosen scale */
  mode: 'fit' | 'fill' | 'custom'
}

export const MIN_SCALE = 0.1
export const MAX_SCALE = 8
export const ZOOM_STEP = 1.25

export const INITIAL_ZOOM: ZoomState = {
  scale: 1,
  x: 0,
  y: 0,
  rotate: 0,
  flipH: false,
  flipV: false,
  mode: 'fit',
}

export type ZoomAction =
  | { type: 'fit' }
  | { type: 'fill' }
  | { type: 'original' }
  | { type: 'zoomIn'; at?: Point }
  | { type: 'zoomOut'; at?: Point }
  | { type: 'zoomTo'; scale: number; at?: Point }
  /** multiplicative zoom (pinch / wheel), factor > 1 zooms in */
  | { type: 'zoomBy'; factor: number; at?: Point }
  | { type: 'pan'; dx: number; dy: number }
  | { type: 'rotate'; dir: 1 | -1 }
  | { type: 'flipH' }
  | { type: 'flipV' }
  | { type: 'reset' }
  /** viewport or image size changed: re-apply fit/fill, clamp custom */
  | { type: 'layout' }

/** Size of the image box after rotation (90/270 swap the sides). */
export function rotatedSize(natural: Box, rotate: Rotation): Box {
  return rotate === 90 || rotate === 270 ? { w: natural.h, h: natural.w } : natural
}

/** Scale that fits (whole image visible) or fills (no empty space) the viewport. */
export function fitScale(
  viewport: Box,
  natural: Box,
  rotate: Rotation,
  mode: 'fit' | 'fill',
): number {
  const r = rotatedSize(natural, rotate)
  if (r.w <= 0 || r.h <= 0 || viewport.w <= 0 || viewport.h <= 0) return 1
  const sx = viewport.w / r.w
  const sy = viewport.h / r.h
  const s = mode === 'fit' ? Math.min(sx, sy) : Math.max(sx, sy)
  // fit never enlarges beyond 1:1 (small photos stay sharp); fill may
  return clampScale(mode === 'fit' ? Math.min(s, 1) : s)
}

export function clampScale(s: number): number {
  if (!Number.isFinite(s) || s <= 0) return 1
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, s))
}

/** Keep the image inside the viewport: centred when smaller, edges never
 *  dragged past the viewport edges when larger. */
export function clampPan(state: ZoomState, viewport: Box, natural: Box): ZoomState {
  const r = rotatedSize(natural, state.rotate)
  const sw = r.w * state.scale
  const sh = r.h * state.scale
  const maxX = Math.max(0, (sw - viewport.w) / 2)
  const maxY = Math.max(0, (sh - viewport.h) / 2)
  // `+ 0` folds the −0 that Math.max(-0, …) yields into +0
  const x = Math.min(maxX, Math.max(-maxX, state.x)) + 0
  const y = Math.min(maxY, Math.max(-maxY, state.y)) + 0
  return x === state.x && y === state.y ? state : { ...state, x, y }
}

/** Zoom to `scale` keeping the image point under `at` (viewport-centre
 *  relative) fixed: t' = p − (p − t)·s'/s. */
function zoomAround(state: ZoomState, scale: number, at: Point | undefined): ZoomState {
  const s = clampScale(scale)
  if (s === state.scale) return { ...state, mode: 'custom' }
  const p = at ?? { x: 0, y: 0 }
  const k = s / state.scale
  return {
    ...state,
    scale: s,
    x: p.x - (p.x - state.x) * k,
    y: p.y - (p.y - state.y) * k,
    mode: 'custom',
  }
}

export function reduceZoom(
  state: ZoomState,
  action: ZoomAction,
  viewport: Box,
  natural: Box,
): ZoomState {
  switch (action.type) {
    case 'fit':
      return {
        ...state,
        scale: fitScale(viewport, natural, state.rotate, 'fit'),
        x: 0,
        y: 0,
        mode: 'fit',
      }
    case 'fill':
      return {
        ...state,
        scale: fitScale(viewport, natural, state.rotate, 'fill'),
        x: 0,
        y: 0,
        mode: 'fill',
      }
    case 'original':
      return clampPan({ ...state, scale: 1, mode: 'custom' }, viewport, natural)
    case 'zoomIn':
      return clampPan(zoomAround(state, state.scale * ZOOM_STEP, action.at), viewport, natural)
    case 'zoomOut':
      return clampPan(zoomAround(state, state.scale / ZOOM_STEP, action.at), viewport, natural)
    case 'zoomTo':
      return clampPan(zoomAround(state, action.scale, action.at), viewport, natural)
    case 'zoomBy':
      return clampPan(zoomAround(state, state.scale * action.factor, action.at), viewport, natural)
    case 'pan':
      return clampPan(
        { ...state, x: state.x + action.dx, y: state.y + action.dy },
        viewport,
        natural,
      )
    case 'rotate': {
      const rotate = (((state.rotate + action.dir * 90) % 360) + 360) % 360
      const next = { ...state, rotate: rotate as Rotation }
      if (next.mode !== 'custom') return reduceZoom(next, { type: next.mode }, viewport, natural)
      return clampPan(next, viewport, natural)
    }
    case 'flipH':
      return { ...state, flipH: !state.flipH }
    case 'flipV':
      return { ...state, flipV: !state.flipV }
    case 'reset':
      return reduceZoom(INITIAL_ZOOM, { type: 'fit' }, viewport, natural)
    case 'layout':
      if (state.mode !== 'custom') return reduceZoom(state, { type: state.mode }, viewport, natural)
      return clampPan(state, viewport, natural)
  }
}

/** CSS transform for the <img>. */
export function transformOf(state: ZoomState): string {
  const fx = state.flipH ? -1 : 1
  const fy = state.flipV ? -1 : 1
  return `translate(${round(state.x)}px, ${round(state.y)}px) scale(${state.scale}) rotate(${state.rotate}deg) scale(${fx}, ${fy})`
}

function round(n: number): number {
  return Math.round(n * 100) / 100
}

/** Percent label, e.g. 100 for 1:1. */
export function zoomPercent(state: ZoomState): number {
  return Math.round(state.scale * 100)
}
