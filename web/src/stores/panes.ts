import { create } from 'zustand'
import type { SortPref } from '@/features/browser/sortCycle'
import { createTabsStore, MAX_TABS, type TabsState } from './tabs'

/** Dual-pane mode (SPEC §9.3 "varian dual-pane", Step 23b).
 *
 *  The left pane is the classic browser: URL-driven, `useTabsStore`, `tabs.open`.
 *  The right pane owns the same tab model (≤ 10 tabs, history, scroll offsets)
 *  in `useRightTabsStore`, never in the URL; its tabs are persisted in the
 *  `dualpane.right` preference. Exactly one pane is focused; the shared chrome
 *  (toolbar, shortcuts, palette context, preview panel) follows it. */

export type PaneId = 'left' | 'right'

/** Desktop only (SPEC §9.9): below this width the UI falls back to one pane. */
export const DUAL_PANE_QUERY = '(min-width: 1280px)'

export const useRightTabsStore = createTabsStore('rtab')

export interface PanesState {
  focused: PaneId
  /** Session-only sort override of the right pane (does not touch `view.sort`). */
  rightSort: SortPref | null
  focus: (pane: PaneId) => void
  setRightSort: (sort: SortPref | null) => void
}

export const usePanesStore = create<PanesState>((set) => ({
  focused: 'left',
  rightSort: null,
  focus: (pane) => set((s) => (s.focused === pane ? s : { focused: pane })),
  setRightSort: (rightSort) => set({ rightSort }),
}))

export function otherPane(pane: PaneId): PaneId {
  return pane === 'left' ? 'right' : 'left'
}

/** Dual-pane is shown only when enabled, on a wide screen and not on a phone. */
export function isDualPaneActive(enabled: boolean, wide: boolean, mobile = false): boolean {
  return enabled && wide && !mobile
}

/** Shape of the `dualpane.right` preference (validated by `httpapi/prefs.go`). */
export interface DualPaneRightPref {
  tabs: { path: string }[]
  active?: number
}

/** Parses a stored `dualpane.right` value; null when absent or malformed. */
export function parseRightPref(raw: unknown): DualPaneRightPref | null {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null
  const v = raw as { tabs?: unknown; active?: unknown }
  if (!Array.isArray(v.tabs)) return null
  const tabs = v.tabs
    .map((t: unknown) =>
      t && typeof t === 'object' && typeof (t as { path?: unknown }).path === 'string'
        ? (t as { path: string }).path
        : null,
    )
    .filter((p): p is string => !!p && p.startsWith('/'))
    .slice(0, MAX_TABS)
    .map((path) => ({ path }))
  if (tabs.length === 0) return null
  const active =
    typeof v.active === 'number' && Number.isInteger(v.active) && v.active < tabs.length
      ? Math.max(0, v.active)
      : 0
  return { tabs, active }
}

/** The persisted form of the right pane's tabs. */
export function rightPrefOf(state: Pick<TabsState, 'tabs' | 'activeId'>): DualPaneRightPref {
  const tabs = state.tabs.slice(0, MAX_TABS).map((t) => ({ path: t.path }))
  const i = state.tabs.findIndex((t) => t.id === state.activeId)
  return { tabs, active: Math.max(0, Math.min(i, tabs.length - 1)) }
}

let hydrated = false

/** Restores the right pane from `dualpane.right` once per page load (a later
 *  remount of the browser keeps the live state). Returns true when restored. */
export function hydrateRightPane(raw: unknown): boolean {
  if (hydrated) return false
  hydrated = true
  const pref = parseRightPref(raw)
  if (!pref) return false
  useRightTabsStore.getState().restore(
    pref.tabs.map((t) => t.path),
    pref.active ?? 0,
  )
  return true
}

export function isRightPaneHydrated(): boolean {
  return hydrated
}

/** Test helper. */
export function resetPanes(): void {
  hydrated = false
  usePanesStore.setState({ focused: 'left', rightSort: null })
  useRightTabsStore.getState().restore(['/'], 0)
}
