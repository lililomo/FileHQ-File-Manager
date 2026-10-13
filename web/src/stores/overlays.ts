import { create } from 'zustand'

/** Global overlays that can be opened from anywhere (top bar, shortcuts, palette). */
export interface OverlaysState {
  paletteOpen: boolean
  helpOpen: boolean
  setPalette: (open: boolean) => void
  setHelp: (open: boolean) => void
}

export const useOverlaysStore = create<OverlaysState>((set) => ({
  paletteOpen: false,
  helpOpen: false,
  setPalette: (paletteOpen) => set({ paletteOpen }),
  setHelp: (helpOpen) => set({ helpOpen }),
}))
