/** Theme/accent/density application (SPEC §9.1). Mirrors public/theme-init.js,
 *  which applies the stored values before first paint. */

export const THEME_PREFS = ['light', 'dark', 'system'] as const
export type ThemePref = (typeof THEME_PREFS)[number]
export type ResolvedTheme = 'light' | 'dark'
export const ACCENTS = ['blue', 'emerald', 'violet', 'amber', 'rose', 'slate'] as const
export type Accent = (typeof ACCENTS)[number]
export const DENSITIES = ['compact', 'comfortable'] as const
export type Density = (typeof DENSITIES)[number]

export const STORAGE_KEYS = {
  theme: 'theme',
  accent: 'accent',
  density: 'density',
  lang: 'lang',
} as const

function storage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}
export function readStored(key: keyof typeof STORAGE_KEYS): string | null {
  return storage()?.getItem(STORAGE_KEYS[key]) ?? null
}
function writeStored(key: keyof typeof STORAGE_KEYS, value: string | null): void {
  const s = storage()
  if (!s) return
  if (value === null) s.removeItem(STORAGE_KEYS[key])
  else s.setItem(STORAGE_KEYS[key], value)
}

export function isThemePref(v: unknown): v is ThemePref {
  return typeof v === 'string' && (THEME_PREFS as readonly string[]).includes(v)
}
export function isAccent(v: unknown): v is Accent {
  return typeof v === 'string' && (ACCENTS as readonly string[]).includes(v)
}
export function isDensity(v: unknown): v is Density {
  return typeof v === 'string' && (DENSITIES as readonly string[]).includes(v)
}

function systemQuery(): MediaQueryList | null {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null
}

export function resolveTheme(pref: ThemePref): ResolvedTheme {
  if (pref === 'light' || pref === 'dark') return pref
  return systemQuery()?.matches ? 'dark' : 'light'
}

export function applyTheme(pref: ThemePref, persist = true): ResolvedTheme {
  const resolved = resolveTheme(pref)
  const root = document.documentElement
  root.dataset.theme = resolved
  root.dataset.themePref = pref
  if (persist) writeStored('theme', pref)
  return resolved
}

export function applyAccent(accent: Accent, persist = true): void {
  const root = document.documentElement
  if (accent === 'blue') delete root.dataset.accent
  else root.dataset.accent = accent
  if (persist) writeStored('accent', accent === 'blue' ? null : accent)
}

export function applyDensity(density: Density, persist = true): void {
  const root = document.documentElement
  if (density === 'compact') delete root.dataset.density
  else root.dataset.density = density
  if (persist) writeStored('density', density === 'compact' ? null : density)
}

/** Re-resolve when the OS theme changes while pref = system. Returns an unsubscribe. */
export function watchSystemTheme(onChange: (resolved: ResolvedTheme) => void): () => void {
  const q = systemQuery()
  if (!q) return () => {}
  const handler = (e: MediaQueryListEvent) => onChange(e.matches ? 'dark' : 'light')
  q.addEventListener('change', handler)
  return () => q.removeEventListener('change', handler)
}
