import { create } from 'zustand'
import {
  applyAccent,
  applyDensity,
  applyTheme,
  isAccent,
  isDensity,
  isThemePref,
  readStored,
  resolveTheme,
  type Accent,
  type Density,
  type ResolvedTheme,
  type ThemePref,
} from '@/lib/theme'

export interface UIState {
  theme: ThemePref
  resolvedTheme: ResolvedTheme
  accent: Accent
  density: Density
  sidebarCollapsed: boolean
  setTheme: (pref: ThemePref) => void
  setResolvedTheme: (resolved: ResolvedTheme) => void
  setAccent: (accent: Accent) => void
  setDensity: (density: Density) => void
  toggleSidebar: () => void
  /** Apply preferences coming from the server (`/auth/me` prefs); persists locally too. */
  applyServerPrefs: (prefs: Record<string, unknown> | undefined) => void
}

function initialTheme(): ThemePref {
  const v = readStored('theme')
  return isThemePref(v) ? v : 'system'
}
function initialAccent(): Accent {
  const v = readStored('accent')
  return isAccent(v) ? v : 'blue'
}
function initialDensity(): Density {
  const v = readStored('density')
  return isDensity(v) ? v : 'compact'
}

export const useUIStore = create<UIState>((set, get) => ({
  theme: initialTheme(),
  resolvedTheme: resolveTheme(initialTheme()),
  accent: initialAccent(),
  density: initialDensity(),
  sidebarCollapsed: false,
  setTheme: (pref) => {
    const resolved = applyTheme(pref)
    set({ theme: pref, resolvedTheme: resolved })
  },
  setResolvedTheme: (resolved) => {
    if (get().theme === 'system') {
      document.documentElement.dataset.theme = resolved
      set({ resolvedTheme: resolved })
    }
  },
  setAccent: (accent) => {
    applyAccent(accent)
    set({ accent })
  },
  setDensity: (density) => {
    applyDensity(density)
    set({ density })
  },
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
  applyServerPrefs: (prefs) => {
    if (!prefs) return
    const theme = prefs['ui.theme']
    if (isThemePref(theme) && theme !== get().theme) get().setTheme(theme)
    const accent = prefs['ui.accent']
    if (isAccent(accent) && accent !== get().accent) get().setAccent(accent)
    const density = prefs['ui.density']
    if (isDensity(density) && density !== get().density) get().setDensity(density)
    const collapsed = prefs['ui.sidebar_collapsed']
    if (typeof collapsed === 'boolean' && collapsed !== get().sidebarCollapsed) {
      set({ sidebarCollapsed: collapsed })
    }
  },
}))
