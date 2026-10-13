import type { SortDir, SortField } from './api'

/** `view.sort` preference shape (SPEC §8.18). */
export interface SortPref {
  field: SortField
  dir: SortDir
  field2?: SortField
  dir2?: SortDir
}

export const DEFAULT_SORT: SortPref = { field: 'name', dir: 'asc' }

export function isSortField(v: unknown): v is SortField {
  return (
    typeof v === 'string' &&
    [
      'name',
      'size',
      'type',
      'ext',
      'mtime',
      'btime',
      'atime',
      'mode',
      'note',
      'dimensions',
      'duration',
    ].includes(v)
  )
}

export function normalizeSort(v: unknown): SortPref {
  if (!v || typeof v !== 'object') return DEFAULT_SORT
  const o = v as Record<string, unknown>
  if (!isSortField(o.field)) return DEFAULT_SORT
  const out: SortPref = { field: o.field, dir: o.dir === 'desc' ? 'desc' : 'asc' }
  if (isSortField(o.field2) && o.field2 !== o.field) {
    out.field2 = o.field2
    out.dir2 = o.dir2 === 'desc' ? 'desc' : 'asc'
  }
  return out
}

/** Header click cycle (SPEC §8.3): asc → desc → default (name asc). With `secondary`
 *  (Shift+click) the field becomes/cycles the secondary sort: asc → desc → none. */
export function cycleSort(current: SortPref, field: SortField, secondary = false): SortPref {
  if (secondary) {
    if (field === current.field) return current
    if (current.field2 !== field) return { ...current, field2: field, dir2: 'asc' }
    if (current.dir2 === 'asc') return { ...current, dir2: 'desc' }
    const { field2: _f, dir2: _d, ...rest } = current
    void _f
    void _d
    return rest
  }
  if (current.field !== field) {
    const next: SortPref = { field, dir: 'asc' }
    if (current.field2 && current.field2 !== field) {
      next.field2 = current.field2
      next.dir2 = current.dir2 ?? 'asc'
    }
    return next
  }
  if (current.dir === 'asc') return { ...current, dir: 'desc' }
  // third click: back to default, secondary cleared
  return DEFAULT_SORT
}

export function sameSort(a: SortPref, b: SortPref): boolean {
  return a.field === b.field && a.dir === b.dir && a.field2 === b.field2 && a.dir2 === b.dir2
}
