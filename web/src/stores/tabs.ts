import { create } from 'zustand'
import { ROOT } from '@/features/browser/path'

/** Folder tabs with per-tab navigation history (SPEC §8.3 / PROMPT 7a+7b).
 *  The URL stays the source of truth: switching tabs navigates to the tab's
 *  path, and `visit()` records the path the router landed on. */
export interface Tab {
  id: string
  path: string
  history: string[]
  index: number
  /** scroll offset per visited path, restored when navigating back */
  scroll: Record<string, number>
}

export const MAX_TABS = 10

export interface TabsState {
  tabs: Tab[]
  activeId: string
  /** Path the store expects next because back/forward was requested (consumed by `visit`). */
  pending: { path: string; index: number } | null
  active: () => Tab
  /** Called when the URL path changes. Pushes onto the history unless it is the
   *  pending back/forward target or the current entry. */
  visit: (path: string) => void
  /** Returns the target path for back/forward (or null) and marks it pending. */
  back: () => string | null
  forward: () => string | null
  canBack: () => boolean
  canForward: () => boolean
  setScroll: (path: string, offset: number) => void
  /** Open a tab (activated unless `background`); null when MAX_TABS is reached. */
  openTab: (path: string, opts?: { background?: boolean }) => Tab | null
  /** Close a tab; returns the path of the tab that is active afterwards. */
  closeTab: (id: string) => string
  closeOthers: (id: string) => void
  closeRight: (id: string) => void
  /** Activate a tab and return its path. */
  activate: (id: string) => string
  /** Move a tab to another position. */
  reorder: (id: string, toIndex: number) => void
  /** Replace all tabs with the persisted list (`tabs.open`), making sure the
   *  tab for `currentPath` exists and is active. */
  hydrate: (paths: string[], currentPath: string) => void
}

const MAX_HISTORY = 100
let seq = 1

function makeTab(path: string): Tab {
  return { id: `tab-${seq++}`, path, history: [path], index: 0, scroll: {} }
}

export const useTabsStore = create<TabsState>((set, get) => ({
  tabs: [makeTab(ROOT)],
  activeId: 'tab-1',
  pending: null,
  active: () => {
    const s = get()
    return s.tabs.find((t) => t.id === s.activeId) ?? s.tabs[0]!
  },
  visit: (path) => {
    const s = get()
    const tab = s.active()
    if (s.pending && s.pending.path === path) {
      set({
        pending: null,
        tabs: s.tabs.map((t) => (t.id === tab.id ? { ...t, path, index: s.pending!.index } : t)),
      })
      return
    }
    if (tab.history[tab.index] === path) {
      if (tab.path !== path)
        set({ tabs: s.tabs.map((t) => (t.id === tab.id ? { ...t, path } : t)) })
      return
    }
    let history = [...tab.history.slice(0, tab.index + 1), path]
    if (history.length > MAX_HISTORY) history = history.slice(history.length - MAX_HISTORY)
    set({
      pending: null,
      tabs: s.tabs.map((t) =>
        t.id === tab.id ? { ...t, path, history, index: history.length - 1 } : t,
      ),
    })
  },
  back: () => {
    const tab = get().active()
    if (tab.index <= 0) return null
    const index = tab.index - 1
    const path = tab.history[index]!
    set({ pending: { path, index } })
    return path
  },
  forward: () => {
    const tab = get().active()
    if (tab.index >= tab.history.length - 1) return null
    const index = tab.index + 1
    const path = tab.history[index]!
    set({ pending: { path, index } })
    return path
  },
  canBack: () => get().active().index > 0,
  canForward: () => {
    const t = get().active()
    return t.index < t.history.length - 1
  },
  setScroll: (path, offset) => {
    const s = get()
    const tab = s.active()
    if (tab.scroll[path] === offset) return
    set({
      tabs: s.tabs.map((t) =>
        t.id === tab.id ? { ...t, scroll: { ...t.scroll, [path]: offset } } : t,
      ),
    })
  },
  openTab: (path, opts) => {
    const s = get()
    if (s.tabs.length >= MAX_TABS) return null
    const tab = makeTab(path)
    const i = s.tabs.findIndex((t) => t.id === s.activeId)
    const tabs = [...s.tabs]
    tabs.splice(i + 1, 0, tab)
    set({ tabs, activeId: opts?.background ? s.activeId : tab.id, pending: null })
    return tab
  },
  closeTab: (id) => {
    const s = get()
    if (s.tabs.length === 1) {
      // last tab: keep one tab, reset it to the root
      const fresh = makeTab(ROOT)
      set({ tabs: [fresh], activeId: fresh.id, pending: null })
      return fresh.path
    }
    const i = s.tabs.findIndex((t) => t.id === id)
    if (i < 0) return s.active().path
    const tabs = s.tabs.filter((t) => t.id !== id)
    let activeId = s.activeId
    if (id === s.activeId) activeId = (tabs[i] ?? tabs[i - 1])!.id
    set({ tabs, activeId, pending: null })
    return tabs.find((t) => t.id === activeId)!.path
  },
  closeOthers: (id) => {
    const s = get()
    const keep = s.tabs.find((t) => t.id === id)
    if (!keep) return
    set({ tabs: [keep], activeId: keep.id, pending: null })
  },
  closeRight: (id) => {
    const s = get()
    const i = s.tabs.findIndex((t) => t.id === id)
    if (i < 0) return
    const tabs = s.tabs.slice(0, i + 1)
    const activeId = tabs.some((t) => t.id === s.activeId) ? s.activeId : id
    set({ tabs, activeId, pending: null })
  },
  activate: (id) => {
    const s = get()
    const tab = s.tabs.find((t) => t.id === id)
    if (!tab) return s.active().path
    set({ activeId: id, pending: null })
    return tab.path
  },
  reorder: (id, toIndex) => {
    const s = get()
    const from = s.tabs.findIndex((t) => t.id === id)
    if (from < 0) return
    const tabs = [...s.tabs]
    const [tab] = tabs.splice(from, 1)
    tabs.splice(Math.max(0, Math.min(tabs.length, toIndex)), 0, tab!)
    set({ tabs })
  },
  hydrate: (paths, currentPath) => {
    // duplicates are legitimate (the same folder opened in two tabs)
    const valid = paths.filter((p) => typeof p === 'string' && p.startsWith('/'))
    const tabs = valid.slice(0, MAX_TABS).map(makeTab)
    let active = tabs.find((t) => t.path === currentPath)
    if (!active) {
      // The URL the browser landed on is not an open tab: continue in the first
      // restored tab (its previous path stays reachable with Back) instead of
      // opening yet another tab on every reload.
      const first = tabs[0]
      if (first) {
        active = { ...first, path: currentPath, history: [first.path, currentPath], index: 1 }
        tabs[0] = active
      } else {
        active = makeTab(currentPath)
        tabs.push(active)
      }
    }
    set({ tabs, activeId: active.id, pending: null })
  },
}))
