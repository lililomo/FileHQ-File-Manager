import { describe, expect, it } from 'vitest'
import {
  INITIAL_ZOOM,
  MAX_SCALE,
  MIN_SCALE,
  clampPan,
  fitScale,
  reduceZoom,
  rotatedSize,
  transformOf,
  zoomPercent,
  type ZoomState,
} from './zoomPan'

const viewport = { w: 1000, h: 600 }
const big = { w: 4000, h: 3000 } // 24 MP-ish landscape
const small = { w: 300, h: 200 }

const run = (state: ZoomState, ...actions: Parameters<typeof reduceZoom>[1][]) =>
  actions.reduce((s, a) => reduceZoom(s, a, viewport, big), state)

describe('fit / fill / original', () => {
  it('fit shows the whole image and never enlarges', () => {
    expect(fitScale(viewport, big, 0, 'fit')).toBeCloseTo(0.2) // 600/3000
    expect(fitScale(viewport, small, 0, 'fit')).toBe(1)
    expect(fitScale(viewport, big, 0, 'fill')).toBeCloseTo(0.25) // 1000/4000
  })
  it('fit uses the rotated box for 90°', () => {
    expect(rotatedSize(big, 90)).toEqual({ w: 3000, h: 4000 })
    expect(fitScale(viewport, big, 90, 'fit')).toBeCloseTo(0.15) // 600/4000
  })
  it('original is 1:1 and centred', () => {
    const s = run(INITIAL_ZOOM, { type: 'fit' }, { type: 'original' })
    expect(s.scale).toBe(1)
    expect(s.mode).toBe('custom')
    expect(zoomPercent(s)).toBe(100)
  })
})

describe('zoom', () => {
  it('steps by 1.25 and clamps to the range', () => {
    let s = run(INITIAL_ZOOM, { type: 'fit' })
    const start = s.scale
    s = run(s, { type: 'zoomIn' })
    expect(s.scale).toBeCloseTo(start * 1.25)
    s = run(s, { type: 'zoomOut' })
    expect(s.scale).toBeCloseTo(start)
    for (let i = 0; i < 40; i++) s = run(s, { type: 'zoomIn' })
    expect(s.scale).toBe(MAX_SCALE)
    for (let i = 0; i < 80; i++) s = run(s, { type: 'zoomOut' })
    expect(s.scale).toBe(MIN_SCALE)
  })
  it('zooming around a point keeps that image point under the cursor', () => {
    // 1:1, image centred; the pixel under viewport point (200, 100) is image point (200, 100)
    let s = run(INITIAL_ZOOM, { type: 'original' })
    s = reduceZoom(s, { type: 'zoomTo', scale: 2, at: { x: 200, y: 100 } }, viewport, big)
    // image point u = (p − t)/s must be unchanged: (200 − x)/2 = 200 → x = −200
    expect(s.x).toBeCloseTo(-200)
    expect(s.y).toBeCloseTo(-100)
    expect(s.mode).toBe('custom')
  })
  it('wheel/pinch zoomBy is multiplicative', () => {
    const s = run(run(INITIAL_ZOOM, { type: 'original' }), { type: 'zoomBy', factor: 1.5 })
    expect(s.scale).toBeCloseTo(1.5)
  })
})

describe('pan clamping', () => {
  it('centres an image smaller than the viewport', () => {
    const s = clampPan({ ...INITIAL_ZOOM, scale: 0.1, x: 500, y: -300 }, viewport, big)
    expect(s.x).toBe(0)
    expect(s.y).toBe(0)
  })
  it('stops the edges of a larger image inside the viewport', () => {
    // at 1:1 the 4000×3000 image overflows by 3000×2400 → max offset 1500×1200
    const s = run(run(INITIAL_ZOOM, { type: 'original' }), { type: 'pan', dx: 99999, dy: -99999 })
    expect(s.x).toBe(1500)
    expect(s.y).toBe(-1200)
  })
})

describe('rotate / flip / layout', () => {
  it('rotation cycles through 0/90/180/270 both ways and refits', () => {
    let s = run(INITIAL_ZOOM, { type: 'fit' })
    s = run(s, { type: 'rotate', dir: 1 })
    expect(s.rotate).toBe(90)
    expect(s.scale).toBeCloseTo(0.15)
    s = run(s, { type: 'rotate', dir: -1 }, { type: 'rotate', dir: -1 })
    expect(s.rotate).toBe(270)
    s = run(s, { type: 'rotate', dir: 1 })
    expect(s.rotate).toBe(0)
    expect(s.mode).toBe('fit')
  })
  it('flips toggle and show in the transform', () => {
    const s = run(INITIAL_ZOOM, { type: 'flipH' }, { type: 'flipV' })
    expect(s.flipH && s.flipV).toBe(true)
    expect(transformOf(s)).toContain('scale(-1, -1)')
    expect(transformOf(run(s, { type: 'flipH' }))).toContain('scale(1, -1)')
  })
  it('layout re-applies fit after a viewport change but keeps a custom zoom', () => {
    const fitted = reduceZoom(INITIAL_ZOOM, { type: 'fit' }, viewport, big)
    const narrower = reduceZoom(fitted, { type: 'layout' }, { w: 400, h: 600 }, big)
    expect(narrower.scale).toBeCloseTo(0.1)
    const custom = reduceZoom(fitted, { type: 'zoomTo', scale: 0.5 }, viewport, big)
    expect(reduceZoom(custom, { type: 'layout' }, { w: 400, h: 600 }, big).scale).toBe(0.5)
  })
  it('reset returns to fit', () => {
    const s = run(INITIAL_ZOOM, { type: 'original' }, { type: 'rotate', dir: 1 }, { type: 'reset' })
    expect(s).toEqual(reduceZoom(INITIAL_ZOOM, { type: 'fit' }, viewport, big))
  })
})
