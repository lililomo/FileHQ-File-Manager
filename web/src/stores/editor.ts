import { create } from 'zustand'
import type { EOL } from '@/features/editor/api'

/** Editor workspace tabs (SPEC §8.13 "Tab & sesi"). Content lives in Monaco
 *  models (features/editor/models.ts); the store holds metadata, dirty
 *  state and the restore positions. Max 20 tabs. */

export const MAX_EDITOR_TABS = 20

export type TabStatus = 'loading' | 'ready' | 'binary' | 'error'

export interface EditorTab {
  path: string
  name: string
  language: string
  encoding: string
  detectedEncoding: string
  bom: boolean
  eol: EOL
  lossy: boolean
  readOnly: boolean
  truncated: boolean
  tailLines: number
  size: number
  mtime: string
  etag: string
  lines: number
  status: TabStatus
  error: { code: string; message: string } | null
  dirty: boolean
  saving: boolean
  cursor: { line: number; col: number }
  scrollTop: number
  /** Position to jump to once the content is in (deep link / search result); len selects a match. */
  pending: { line: number; col?: number; len?: number } | null
  /** Draft found in IndexedDB that differs from the server content. */
  draft: { content: string; etag: string; at: number } | null
  /** Conflict reported by the last save (409). */
  conflict: { serverEtag: string; serverMtime: string } | null
  /** Step 16b: a read-only tab showing one version of `path` (no live file behind it). */
  versionId: string | null
  versionNo: number
  /** Number of stored versions of the live file (null = unknown yet). */
  versions: number | null
  /** Markdown preview pane open. */
  preview: boolean
}

export interface OpenOptions {
  line?: number
  col?: number
  /** Length of the match to select at line/col (find in files). */
  len?: number
  activate?: boolean
  encoding?: string
  /** Open a stored version read-only (tab key = `${path}#v${versionId}`). */
  version?: { id: string; no: number }
}

export interface EditorState {
  tabs: EditorTab[]
  activePath: string | null
  hydrated: boolean
  /** Open a tab (or focus it). Returns false when the cap is reached. */
  open: (path: string, opts?: OpenOptions) => boolean
  close: (path: string) => void
  closeOthers: (path: string) => void
  closeRight: (path: string) => void
  closeAll: () => void
  activate: (path: string | null) => void
  reorder: (from: number, to: number) => void
  patch: (path: string, patch: Partial<EditorTab>) => void
  hydrate: (
    tabs: Pick<EditorTab, 'path' | 'cursor' | 'scrollTop' | 'language' | 'encoding'>[],
    active: string | null,
  ) => void
  reset: () => void
}

function baseName(path: string): string {
  return path.split('/').pop() || path
}

export function newTab(path: string, extra: Partial<EditorTab> = {}): EditorTab {
  return {
    path,
    name: baseName(path),
    language: 'plaintext',
    encoding: 'utf-8',
    detectedEncoding: 'utf-8',
    bom: false,
    eol: 'lf',
    lossy: false,
    readOnly: false,
    truncated: false,
    tailLines: 0,
    size: 0,
    mtime: '',
    etag: '',
    lines: 0,
    status: 'loading',
    error: null,
    dirty: false,
    saving: false,
    cursor: { line: 1, col: 1 },
    scrollTop: 0,
    pending: null,
    draft: null,
    conflict: null,
    versionId: null,
    versionNo: 0,
    versions: null,
    preview: false,
    ...extra,
  }
}

function nextActive(
  tabs: EditorTab[],
  closedIndex: number,
  closedPath: string,
  active: string | null,
): string | null {
  if (active !== closedPath) return active
  if (tabs.length === 0) return null
  const i = Math.min(closedIndex, tabs.length - 1)
  return tabs[i]!.path
}

export const useEditorStore = create<EditorState>((set, get) => ({
  tabs: [],
  activePath: null,
  hydrated: false,
  open: (path, opts = {}) => {
    const { tabs } = get()
    const existing = tabs.find((t) => t.path === path)
    const pending = opts.line
      ? {
          line: opts.line,
          ...(opts.col ? { col: opts.col } : {}),
          ...(opts.len ? { len: opts.len } : {}),
        }
      : null
    if (existing) {
      set({
        tabs: tabs.map((t) => (t.path === path ? { ...t, pending: pending ?? t.pending } : t)),
        activePath: opts.activate === false ? get().activePath : path,
      })
      return true
    }
    if (tabs.length >= MAX_EDITOR_TABS) return false
    const tab = newTab(path, {
      pending,
      ...(opts.encoding ? { encoding: opts.encoding } : {}),
      ...(opts.version
        ? {
            versionId: opts.version.id,
            versionNo: opts.version.no,
            readOnly: true,
            name: `${baseName(path.split('#v')[0] ?? path)} (v${opts.version.no})`,
          }
        : {}),
    })
    set({
      tabs: [...tabs, tab],
      activePath: opts.activate === false ? (get().activePath ?? path) : path,
    })
    return true
  },
  close: (path) => {
    const { tabs, activePath } = get()
    const i = tabs.findIndex((t) => t.path === path)
    if (i < 0) return
    const rest = tabs.filter((t) => t.path !== path)
    set({ tabs: rest, activePath: nextActive(rest, i, path, activePath) })
  },
  closeOthers: (path) => {
    const keep = get().tabs.filter((t) => t.path === path)
    set({ tabs: keep, activePath: keep.length ? path : null })
  },
  closeRight: (path) => {
    const { tabs, activePath } = get()
    const i = tabs.findIndex((t) => t.path === path)
    if (i < 0) return
    const kept = tabs.slice(0, i + 1)
    const active = kept.some((t) => t.path === activePath) ? activePath : path
    set({ tabs: kept, activePath: active })
  },
  closeAll: () => set({ tabs: [], activePath: null }),
  activate: (path) => {
    if (path === null || get().tabs.some((t) => t.path === path)) set({ activePath: path })
  },
  reorder: (from, to) => {
    const tabs = [...get().tabs]
    if (from < 0 || from >= tabs.length || to < 0 || to >= tabs.length || from === to) return
    const [moved] = tabs.splice(from, 1)
    tabs.splice(to, 0, moved!)
    set({ tabs })
  },
  patch: (path, patch) =>
    set((s) => ({ tabs: s.tabs.map((t) => (t.path === path ? { ...t, ...patch } : t)) })),
  hydrate: (saved, active) => {
    const { tabs } = get()
    const known = new Set(tabs.map((t) => t.path))
    const restored = saved
      .filter((s) => !known.has(s.path))
      .slice(0, Math.max(0, MAX_EDITOR_TABS - tabs.length))
      .map((s) =>
        newTab(s.path, {
          cursor: s.cursor,
          scrollTop: s.scrollTop,
          language: s.language,
          encoding: s.encoding,
        }),
      )
    const all = [...restored, ...tabs]
    const activePath =
      get().activePath ??
      (active && all.some((t) => t.path === active) ? active : (all[0]?.path ?? null))
    set({ tabs: all, activePath, hydrated: true })
  },
  reset: () => set({ tabs: [], activePath: null, hydrated: false }),
}))

export function useActiveTab(): EditorTab | null {
  return useEditorStore((s) => s.tabs.find((t) => t.path === s.activePath) ?? null)
}
