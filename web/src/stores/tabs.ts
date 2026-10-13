import { create } from 'zustand'
import { ROOT } from '@/features/browser/path'

/** Per-tab navigation history (SPEC §8.3 / PROMPT 7a: one tab for now, multi-tab in 7b). */
export interface Tab {
  id: string
  path: string
  history: string[]
  index: number
  /** scroll offset per visited path, restored when navigating back */
  scroll: Record<string, number>
}

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
}

const MAX_HISTORY = 100

function makeTab(path: string): Tab {
  return { id: 'tab-1', path, history: [path], index: 0, scroll: {} }
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
}))
