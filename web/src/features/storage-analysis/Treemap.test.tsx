import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { AnalysisNode } from './api'
import { squarify } from './squarify'
import { Treemap } from './Treemap'

describe('squarify', () => {
  it('covers the rectangle exactly with areas proportional to size', () => {
    const items = [6, 6, 4, 3, 2, 2, 1]
    const rect = { x: 0, y: 0, w: 600, h: 400 }
    const out = squarify(items, (v) => v, rect)
    expect(out).toHaveLength(items.length)
    const total = items.reduce((a, b) => a + b, 0)
    let area = 0
    for (const { item, rect: r } of out) {
      expect(r.x).toBeGreaterThanOrEqual(-1e-6)
      expect(r.y).toBeGreaterThanOrEqual(-1e-6)
      expect(r.x + r.w).toBeLessThanOrEqual(600 + 1e-6)
      expect(r.y + r.h).toBeLessThanOrEqual(400 + 1e-6)
      expect(r.w * r.h).toBeCloseTo((item / total) * 600 * 400, 3)
      area += r.w * r.h
    }
    expect(area).toBeCloseTo(600 * 400, 3)
  })

  it('keeps aspect ratios reasonable (the squarified property)', () => {
    const items = Array.from({ length: 20 }, (_, i) => 100 - i * 4)
    const out = squarify(items, (v) => v, { x: 10, y: 10, w: 500, h: 300 })
    for (const { rect } of out) {
      const ratio = Math.max(rect.w / rect.h, rect.h / rect.w)
      expect(ratio).toBeLessThan(8)
    }
  })

  it('skips zero sizes and empty input', () => {
    expect(squarify([0, 0], (v) => v, { x: 0, y: 0, w: 10, h: 10 })).toEqual([])
    expect(squarify([], (v: number) => v, { x: 0, y: 0, w: 10, h: 10 })).toEqual([])
    const out = squarify([5, 0, 5], (v) => v, { x: 0, y: 0, w: 10, h: 10 })
    expect(out.map((o) => o.item)).toEqual([5, 5])
  })
})

const tree: AnalysisNode = {
  name: '/',
  path: '/',
  kind: 'dir',
  size: 1000,
  files: 5,
  dirs: 2,
  children: [
    {
      name: 'foto',
      path: '/foto',
      kind: 'dir',
      size: 600,
      files: 3,
      dirs: 0,
      children: [{ name: '', path: '/foto', kind: 'files', size: 600, files: 3, dirs: 0 }],
    },
    { name: 'dok', path: '/dok', kind: 'dir', size: 300, files: 1, dirs: 0 },
    { name: '', path: '/', kind: 'files', size: 100, files: 1, dirs: 0 },
  ],
}

describe('Treemap', () => {
  it('renders one cell per node with proportional areas and opens folders on click', () => {
    const opened: string[] = []
    render(<Treemap node={tree} width={400} height={200} onOpen={(n) => opened.push(n.path)} />)
    const cells = screen.getAllByTestId('treemap-cell')
    const top = cells.filter((c) => c.dataset.depth === '0')
    expect(top).toHaveLength(3)
    const area = (el: HTMLElement) => {
      const r = el.querySelector('rect')!
      return (Number(r.getAttribute('width')) + 1) * (Number(r.getAttribute('height')) + 1)
    }
    const foto = top.find((c) => c.dataset.path === '/foto' && c.dataset.kind === 'dir')!
    const dok = top.find((c) => c.dataset.path === '/dok')!
    expect(area(foto) / area(dok)).toBeCloseTo(2, 0)
    expect(screen.getByRole('button', { name: /foto/ })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /foto/ }))
    expect(opened).toEqual(['/foto'])
    // files leaf is not clickable
    expect(screen.queryByRole('button', { name: /Berkas langsung/ })).toBeNull()
  })
})
