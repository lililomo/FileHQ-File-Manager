/** Squarified treemap layout (Bruls, Huizing & van Wijk 2000): pure
 *  function, no library. Items are laid out in the given order (callers sort
 *  by size desc); zero/negative sizes are skipped. */
export interface Rect {
  x: number
  y: number
  w: number
  h: number
}

export interface LayoutItem<T> {
  item: T
  rect: Rect
}

function worst(row: number[], length: number, area: number): number {
  if (row.length === 0 || length <= 0) return Infinity
  const s = row.reduce((a, b) => a + b, 0)
  if (s <= 0) return Infinity
  const max = Math.max(...row)
  const min = Math.min(...row)
  const w2 = length * length
  const s2 = s * s
  return Math.max((w2 * max) / s2, s2 / (w2 * min)) * (area > 0 ? 1 : 1)
}

/** Lays out `items` (sized by `size`) inside `rect`. */
export function squarify<T>(items: T[], size: (t: T) => number, rect: Rect): LayoutItem<T>[] {
  const out: LayoutItem<T>[] = []
  const entries = items.map((item) => ({ item, v: Math.max(0, size(item)) })).filter((e) => e.v > 0)
  const total = entries.reduce((a, e) => a + e.v, 0)
  if (total <= 0 || rect.w <= 0 || rect.h <= 0) return out
  const area = rect.w * rect.h
  const scaled = entries.map((e) => ({ item: e.item, a: (e.v / total) * area }))

  let x = rect.x
  let y = rect.y
  let w = rect.w
  let h = rect.h
  let row: { item: T; a: number }[] = []
  let i = 0

  const flush = () => {
    if (row.length === 0) return
    const sum = row.reduce((s, r) => s + r.a, 0)
    const horizontal = w >= h // lay the row along the shorter side
    if (horizontal) {
      const rw = sum / h
      let cy = y
      for (const r of row) {
        const rh = r.a / rw
        out.push({ item: r.item, rect: { x, y: cy, w: rw, h: rh } })
        cy += rh
      }
      x += rw
      w -= rw
    } else {
      const rh = sum / w
      let cx = x
      for (const r of row) {
        const rw = r.a / rh
        out.push({ item: r.item, rect: { x: cx, y, w: rw, h: rh } })
        cx += rw
      }
      y += rh
      h -= rh
    }
    row = []
  }

  while (i < scaled.length) {
    const next = scaled[i]!
    const length = Math.min(w, h)
    const current = row.map((r) => r.a)
    if (
      row.length === 0 ||
      worst([...current, next.a], length, area) <= worst(current, length, area)
    ) {
      row.push(next)
      i++
    } else {
      flush()
    }
  }
  flush()
  return out
}
