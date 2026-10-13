/** Pure selection model of the file grid (SPEC §9.5 / PROMPT 7b task 4).
 *
 *  Two modes: `explicit` holds the selected paths; `all` means "everything in the
 *  folder, including pages not loaded yet" minus `excluded` — so Ctrl+A on a
 *  10.000-item folder selects all of it and the status bar can show the total
 *  (count = total − excluded). Range selection uses the loaded, ordered items
 *  and an anchor (the last plain click). */

export interface SelectionState {
  mode: 'explicit' | 'all'
  paths: ReadonlySet<string>
  excluded: ReadonlySet<string>
  anchor: string | null
}

export interface Selectable {
  path: string
  size: number
  isDir: boolean
}

export const EMPTY_SELECTION: SelectionState = {
  mode: 'explicit',
  paths: new Set(),
  excluded: new Set(),
  anchor: null,
}

export function isSelected(s: SelectionState, path: string): boolean {
  return s.mode === 'all' ? !s.excluded.has(path) : s.paths.has(path)
}

export function isEmpty(s: SelectionState, loadedCount: number, total: number): boolean {
  return selectedCount(s, loadedCount, total) === 0
}

/** Number of selected items (uses `total` for the virtual "all" mode). */
export function selectedCount(s: SelectionState, loadedCount: number, total: number): number {
  if (s.mode === 'all') return Math.max(0, Math.max(total, loadedCount) - s.excluded.size)
  return s.paths.size
}

/** Selected items among the loaded ones (size totals, preview, actions). */
export function selectedItems<T extends Selectable>(s: SelectionState, items: T[]): T[] {
  if (s.mode === 'explicit' && s.paths.size === 0) return []
  return items.filter((it) => isSelected(s, it.path))
}

export function selectOnly(path: string): SelectionState {
  return { mode: 'explicit', paths: new Set([path]), excluded: new Set(), anchor: path }
}

export function toggle(s: SelectionState, path: string): SelectionState {
  if (s.mode === 'all') {
    const excluded = new Set(s.excluded)
    if (excluded.has(path)) excluded.delete(path)
    else excluded.add(path)
    return { ...s, excluded, anchor: path }
  }
  const paths = new Set(s.paths)
  if (paths.has(path)) paths.delete(path)
  else paths.add(path)
  return { ...s, paths, anchor: path }
}

/** Shift+click / Shift+arrow: select the range anchor…path (replacing the
 *  selection unless `additive`), keeping the anchor. */
export function selectRange<T extends Selectable>(
  s: SelectionState,
  items: T[],
  path: string,
  additive = false,
): SelectionState {
  const anchor = s.anchor ?? path
  let a = items.findIndex((it) => it.path === anchor)
  let b = items.findIndex((it) => it.path === path)
  if (b < 0) return s
  if (a < 0) a = b
  if (a > b) [a, b] = [b, a]
  const range = items.slice(a, b + 1).map((it) => it.path)
  const paths = new Set<string>(additive ? selectedItems(s, items).map((it) => it.path) : [])
  for (const p of range) paths.add(p)
  return { mode: 'explicit', paths, excluded: new Set(), anchor }
}

export function selectAll(): SelectionState {
  return { mode: 'all', paths: new Set(), excluded: new Set(), anchor: null }
}

/** Ctrl+I: loaded items flip; in "all" mode the excluded set becomes the selection. */
export function invert<T extends Selectable>(s: SelectionState, items: T[]): SelectionState {
  if (s.mode === 'all') {
    return { mode: 'explicit', paths: new Set(s.excluded), excluded: new Set(), anchor: s.anchor }
  }
  if (s.paths.size === 0) return selectAll()
  const paths = new Set<string>()
  for (const it of items) if (!s.paths.has(it.path)) paths.add(it.path)
  return { mode: 'explicit', paths, excluded: new Set(), anchor: s.anchor }
}

export function clear(): SelectionState {
  return EMPTY_SELECTION
}

/** Click with modifiers: plain = select only + anchor; Ctrl = toggle; Shift = range. */
export function click<T extends Selectable>(
  s: SelectionState,
  items: T[],
  path: string,
  mod: { ctrl?: boolean; shift?: boolean },
): SelectionState {
  if (mod.shift) return selectRange(s, items, path, !!mod.ctrl)
  if (mod.ctrl) return toggle(s, path)
  return selectOnly(path)
}

/** Drop paths that no longer exist (after refresh/navigation). */
export function prune<T extends Selectable>(s: SelectionState, items: T[]): SelectionState {
  if (s.mode === 'all') return s
  if (s.paths.size === 0) return s
  const have = new Set(items.map((it) => it.path))
  const paths = new Set([...s.paths].filter((p) => have.has(p)))
  if (paths.size === s.paths.size) return s
  return { ...s, paths, anchor: s.anchor && have.has(s.anchor) ? s.anchor : null }
}

/** Header checkbox state. */
export function headerState(
  s: SelectionState,
  loadedCount: number,
  total: number,
): 'none' | 'some' | 'all' {
  const n = selectedCount(s, loadedCount, total)
  if (n === 0) return 'none'
  if (s.mode === 'all' && s.excluded.size === 0) return 'all'
  if (s.mode === 'explicit' && n >= Math.max(total, loadedCount)) return 'all'
  return 'some'
}
