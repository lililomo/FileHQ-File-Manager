import type { ThumbSize } from '../api'

/** Pure layout math for the grid views (Tiles, Large, Extra large) and the
 *  flowing List view, shared by rendering, keyboard navigation and marquee
 *  selection. */

export interface GridSpec {
  cellW: number
  cellH: number
  gap: number
  padding: number
}

export interface GridLayout extends GridSpec {
  columns: number
  rows: number
  count: number
}

export function gridLayout(spec: GridSpec, width: number, count: number): GridLayout {
  const inner = Math.max(0, width - spec.padding * 2)
  const columns = Math.max(1, Math.floor((inner + spec.gap) / (spec.cellW + spec.gap)))
  const rows = Math.ceil(count / columns)
  return { ...spec, columns, rows, count }
}

export function cellRect(l: GridLayout, index: number) {
  const row = Math.floor(index / l.columns)
  const col = index % l.columns
  return {
    x: l.padding + col * (l.cellW + l.gap),
    y: l.padding + row * (l.cellH + l.gap),
    w: l.cellW,
    h: l.cellH,
  }
}

export interface Rect {
  x: number
  y: number
  w: number
  h: number
}

export function normalizeRect(x1: number, y1: number, x2: number, y2: number): Rect {
  return { x: Math.min(x1, x2), y: Math.min(y1, y2), w: Math.abs(x2 - x1), h: Math.abs(y2 - y1) }
}

function intersects(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.w && a.x + a.w > b.x && a.y < b.y + b.h && a.y + a.h > b.y
}

/** Indices of grid cells intersecting a marquee rectangle (content coords). */
export function cellsInRect(l: GridLayout, r: Rect): number[] {
  if (l.count === 0 || r.w === 0 || r.h === 0) return []
  const stride = l.cellH + l.gap
  const firstRow = Math.max(0, Math.floor((r.y - l.padding) / stride))
  const lastRow = Math.min(l.rows - 1, Math.floor((r.y + r.h - l.padding) / stride))
  const out: number[] = []
  for (let row = firstRow; row <= lastRow; row++) {
    for (let col = 0; col < l.columns; col++) {
      const i = row * l.columns + col
      if (i >= l.count) break
      if (intersects(cellRect(l, i), r)) out.push(i)
    }
  }
  return out
}

/** 2-D keyboard move in a grid; returns the clamped target index. */
export function moveInGrid(
  l: GridLayout,
  from: number,
  key:
    'ArrowLeft' | 'ArrowRight' | 'ArrowUp' | 'ArrowDown' | 'Home' | 'End' | 'PageUp' | 'PageDown',
  pageRows = 1,
): number {
  const last = l.count - 1
  if (last < 0) return 0
  switch (key) {
    case 'ArrowLeft':
      return Math.max(0, from - 1)
    case 'ArrowRight':
      return Math.min(last, from + 1)
    case 'ArrowUp':
      return from - l.columns >= 0 ? from - l.columns : from
    case 'ArrowDown':
      return from + l.columns <= last
        ? from + l.columns
        : Math.min(last, from + l.columns) === from
          ? from
          : last
    case 'Home':
      return 0
    case 'End':
      return last
    case 'PageUp':
      return Math.max(0, from - l.columns * Math.max(1, pageRows))
    case 'PageDown':
      return Math.min(last, from + l.columns * Math.max(1, pageRows))
  }
}

/** Flowing list layout: items fill a column top→bottom, then the next column. */
export interface ListLayout {
  rowsPerColumn: number
  columns: number
  colW: number
  rowH: number
  count: number
}

export function listLayout(height: number, rowH: number, colW: number, count: number): ListLayout {
  const rowsPerColumn = Math.max(1, Math.floor(height / rowH))
  return {
    rowsPerColumn,
    columns: Math.max(1, Math.ceil(count / rowsPerColumn)),
    colW,
    rowH,
    count,
  }
}

export function listCellRect(l: ListLayout, index: number): Rect {
  const col = Math.floor(index / l.rowsPerColumn)
  const row = index % l.rowsPerColumn
  return { x: col * l.colW, y: row * l.rowH, w: l.colW, h: l.rowH }
}

export function listCellsInRect(l: ListLayout, r: Rect): number[] {
  if (l.count === 0 || r.w === 0 || r.h === 0) return []
  const firstCol = Math.max(0, Math.floor(r.x / l.colW))
  const lastCol = Math.min(l.columns - 1, Math.floor((r.x + r.w) / l.colW))
  const firstRow = Math.max(0, Math.floor(r.y / l.rowH))
  const lastRow = Math.min(l.rowsPerColumn - 1, Math.floor((r.y + r.h) / l.rowH))
  const out: number[] = []
  for (let col = firstCol; col <= lastCol; col++) {
    for (let row = firstRow; row <= lastRow; row++) {
      const i = col * l.rowsPerColumn + row
      if (i < l.count && intersects(listCellRect(l, i), r)) out.push(i)
    }
  }
  return out.sort((a, b) => a - b)
}

export function moveInList(
  l: ListLayout,
  from: number,
  key:
    'ArrowLeft' | 'ArrowRight' | 'ArrowUp' | 'ArrowDown' | 'Home' | 'End' | 'PageUp' | 'PageDown',
): number {
  const last = l.count - 1
  if (last < 0) return 0
  switch (key) {
    case 'ArrowUp':
      return Math.max(0, from - 1)
    case 'ArrowDown':
      return Math.min(last, from + 1)
    case 'ArrowLeft':
      return Math.max(0, from - l.rowsPerColumn)
    case 'ArrowRight':
      return Math.min(last, from + l.rowsPerColumn)
    case 'Home':
      return 0
    case 'End':
      return last
    case 'PageUp':
      return Math.max(0, from - l.rowsPerColumn)
    case 'PageDown':
      return Math.min(last, from + l.rowsPerColumn)
  }
}

/** Middle-ellipsis for file names in icon views (SPEC §8.4: `abc…xyz.ext`). */
export function middleEllipsis(name: string, max: number): string {
  if (name.length <= max || max < 5) return name
  const keepEnd = Math.min(8, Math.floor(max / 2))
  const keepStart = max - keepEnd - 1
  return `${name.slice(0, keepStart)}…${name.slice(-keepEnd)}`
}

/** Picks the server thumbnail size for a box. */
export function thumbSizeFor(
  box: number,
  dpr = typeof window !== 'undefined' ? window.devicePixelRatio : 1,
): ThumbSize {
  const px = box * Math.max(1, Math.min(dpr || 1, 3))
  if (px <= 128) return 128
  if (px <= 256) return 256
  return 512
}

/** Cell geometry per view (SPEC §8.4: Tiles 220×64 with 48 px thumbnail,
 *  Large 128 px, Extra large 256 px). */
export const TILES_SPEC: GridSpec = { cellW: 220, cellH: 64, gap: 8, padding: 8 }
export const LARGE_SPEC: GridSpec = { cellW: 148, cellH: 176, gap: 8, padding: 8 }
export const XLARGE_SPEC: GridSpec = { cellW: 276, cellH: 312, gap: 12, padding: 12 }
