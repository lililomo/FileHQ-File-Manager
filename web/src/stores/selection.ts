import { create } from 'zustand'
import { EMPTY_SELECTION, type SelectionState } from '@/features/browser/SelectionModel'

/** Selection per folder tab (SPEC §9.5). Cleared when the tab navigates. */
export interface SelectionStore {
  byTab: Record<string, SelectionState>
  get: (tabId: string) => SelectionState
  set: (tabId: string, next: SelectionState) => void
  update: (tabId: string, fn: (cur: SelectionState) => SelectionState) => void
  clear: (tabId: string) => void
}

export const useSelectionStore = create<SelectionStore>((set, get) => ({
  byTab: {},
  get: (tabId) => get().byTab[tabId] ?? EMPTY_SELECTION,
  set: (tabId, next) => set((s) => ({ byTab: { ...s.byTab, [tabId]: next } })),
  update: (tabId, fn) =>
    set((s) => ({ byTab: { ...s.byTab, [tabId]: fn(s.byTab[tabId] ?? EMPTY_SELECTION) } })),
  clear: (tabId) =>
    set((s) => {
      if (!s.byTab[tabId]) return s
      const byTab = { ...s.byTab }
      delete byTab[tabId]
      return { byTab }
    }),
}))
