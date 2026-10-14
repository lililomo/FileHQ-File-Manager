import { describe, expect, it } from 'vitest'
import {
  cellRect,
  cellsInRect,
  gridLayout,
  listCellsInRect,
  listLayout,
  middleEllipsis,
  moveInGrid,
  moveInList,
} from './gridMath'

describe('gridLayout', () => {
  const spec = { cellW: 100, cellH: 120, gap: 8, padding: 8 }
  it('derives columns from the width and rows from the count', () => {
    const l = gridLayout(spec, 500, 10) // inner 484 → floor(492/108)=4 columns
    expect(l.columns).toBe(4)
    expect(l.rows).toBe(3)
    expect(cellRect(l, 5)).toEqual({ x: 8 + 108, y: 8 + 128, w: 100, h: 120 })
    expect(gridLayout(spec, 10, 3).columns).toBe(1)
  })
  it('marquee picks the intersecting cells only', () => {
    const l = gridLayout(spec, 500, 10)
    // rectangle covering the right half of cell 1 and the left part of cell 2, rows 0 only
    expect(cellsInRect(l, { x: 170, y: 20, w: 100, h: 50 })).toEqual([1, 2])
    // spanning rows 0-1, columns 0-1
    expect(cellsInRect(l, { x: 0, y: 0, w: 220, h: 260 })).toEqual([0, 1, 4, 5])
    expect(cellsInRect(l, { x: 0, y: 0, w: 0, h: 0 })).toEqual([])
    // below the last row: nothing
    expect(cellsInRect(l, { x: 0, y: 1000, w: 50, h: 50 })).toEqual([])
    // last row has 2 cells (8, 9): a wide rect on that row must not return 10/11
    expect(cellsInRect(l, { x: 0, y: 8 + 256, w: 500, h: 10 })).toEqual([8, 9])
  })
  it('keyboard moves in two dimensions and clamps', () => {
    const l = gridLayout(spec, 500, 10)
    expect(moveInGrid(l, 0, 'ArrowRight')).toBe(1)
    expect(moveInGrid(l, 3, 'ArrowDown')).toBe(7)
    expect(moveInGrid(l, 7, 'ArrowDown')).toBe(9) // no cell below → last
    expect(moveInGrid(l, 9, 'ArrowDown')).toBe(9)
    expect(moveInGrid(l, 1, 'ArrowUp')).toBe(1)
    expect(moveInGrid(l, 5, 'ArrowUp')).toBe(1)
    expect(moveInGrid(l, 9, 'ArrowRight')).toBe(9)
    expect(moveInGrid(l, 0, 'End')).toBe(9)
    expect(moveInGrid(l, 9, 'Home')).toBe(0)
    expect(moveInGrid(l, 0, 'PageDown', 2)).toBe(8)
    expect(moveInGrid(l, 8, 'PageUp', 2)).toBe(0)
  })
})

describe('listLayout', () => {
  it('flows top to bottom then left to right', () => {
    const l = listLayout(100, 24, 200, 10) // 4 rows per column → 3 columns
    expect(l.rowsPerColumn).toBe(4)
    expect(l.columns).toBe(3)
    expect(moveInList(l, 0, 'ArrowDown')).toBe(1)
    expect(moveInList(l, 3, 'ArrowRight')).toBe(7)
    expect(moveInList(l, 7, 'ArrowRight')).toBe(9) // clamped to last
    expect(moveInList(l, 9, 'ArrowLeft')).toBe(5)
    expect(listCellsInRect(l, { x: 190, y: 0, w: 20, h: 30 })).toEqual([0, 1, 4, 5])
  })
})

describe('middleEllipsis', () => {
  it('keeps the extension visible', () => {
    expect(middleEllipsis('short.txt', 20)).toBe('short.txt')
    const out = middleEllipsis('laporan-keuangan-tahunan-2026-final.xlsx', 20)
    expect(out).toHaveLength(20)
    expect(out.endsWith('nal.xlsx')).toBe(true)
    expect(out).toContain('…')
  })
})
