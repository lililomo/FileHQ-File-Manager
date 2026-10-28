/** Keep/delete selection of the Duplikat tab (Step 23c). Per group (keyed by
 *  content hash): the copy to keep and the copies marked for removal. The
 *  kept copy can never be marked. */
import type { DuplicateGroup } from './api'

export interface GroupSelection {
  keep: string
  remove: string[]
}

export type DupSelection = Record<string, GroupSelection>

export type DupAction =
  | { type: 'init'; groups: DuplicateGroup[] }
  | { type: 'keep'; hash: string; path: string; group: DuplicateGroup }
  | { type: 'toggle'; hash: string; path: string; on: boolean }
  | { type: 'selectAll'; groups: DuplicateGroup[] }
  | { type: 'clearAll' }

function defaults(g: DuplicateGroup, keep = g.paths[0]?.path ?? ''): GroupSelection {
  return { keep, remove: g.paths.map((f) => f.path).filter((p) => p !== keep) }
}

/** Default selection: keep paths[0], remove every other copy. Groups that
 *  already have a selection keep it (as far as its paths still exist). */
export function initSelection(groups: DuplicateGroup[], prev: DupSelection = {}): DupSelection {
  const out: DupSelection = {}
  for (const g of groups) {
    const old = prev[g.hash]
    const paths = new Set(g.paths.map((f) => f.path))
    if (old && paths.has(old.keep)) {
      out[g.hash] = { keep: old.keep, remove: old.remove.filter((p) => paths.has(p)) }
    } else {
      out[g.hash] = defaults(g)
    }
  }
  return out
}

export function dupReducer(state: DupSelection, action: DupAction): DupSelection {
  switch (action.type) {
    case 'init':
      return initSelection(action.groups, state)
    case 'keep':
      // a new keeper flips the checkboxes: every other copy is removed
      return { ...state, [action.hash]: defaults(action.group, action.path) }
    case 'toggle': {
      const cur = state[action.hash]
      if (!cur || action.path === cur.keep) return state
      const remove = cur.remove.filter((p) => p !== action.path)
      if (action.on) remove.push(action.path)
      return { ...state, [action.hash]: { ...cur, remove } }
    }
    case 'selectAll': {
      const out: DupSelection = { ...state }
      for (const g of action.groups) out[g.hash] = defaults(g, state[g.hash]?.keep)
      return out
    }
    case 'clearAll': {
      const out: DupSelection = {}
      for (const [h, s] of Object.entries(state)) out[h] = { ...s, remove: [] }
      return out
    }
  }
}

export interface DupTotals {
  count: number
  bytes: number
  paths: string[]
  /** Marked copies that are shared (warning before trashing). */
  shared: string[]
  /** One entry per group with marked copies (symlink request). */
  links: { keep: string; paths: string[] }[]
}

export function dupTotals(groups: DuplicateGroup[], sel: DupSelection): DupTotals {
  const out: DupTotals = { count: 0, bytes: 0, paths: [], shared: [], links: [] }
  for (const g of groups) {
    const s = sel[g.hash]
    if (!s || s.remove.length === 0) continue
    const marked = new Set(s.remove)
    const paths: string[] = []
    for (const f of g.paths) {
      if (!marked.has(f.path) || f.path === s.keep) continue
      paths.push(f.path)
      if (f.shared) out.shared.push(f.path)
    }
    if (paths.length === 0) continue
    out.count += paths.length
    out.bytes += paths.length * g.size
    out.paths.push(...paths)
    out.links.push({ keep: s.keep, paths })
  }
  return out
}

/** `?tab=` of `/storage`: anything but "duplicates" is the summary. */
export type StorageTab = 'summary' | 'duplicates'

export function parseStorageSearch(search: Record<string, unknown>): { tab?: 'duplicates' } {
  return search.tab === 'duplicates' ? { tab: 'duplicates' } : {}
}
