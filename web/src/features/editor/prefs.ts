/** Editor preferences (SPEC §8.13 "Pengaturan"): server-side keys `editor.*`
 *  (free-form on the server since Step 7b) with defaults. */
import { useShallow } from 'zustand/react/shallow'
import { usePrefsStore } from '@/stores/prefs'

export const EDITOR_FONTS = [
  'JetBrains Mono',
  'Consolas',
  'Menlo',
  'Ubuntu Mono',
  'monospace',
] as const
export const EDITOR_THEMES = ['system', 'light', 'dark', 'high-contrast'] as const
export type EditorTheme = (typeof EDITOR_THEMES)[number]

export interface EditorPrefs {
  theme: EditorTheme
  font_family: string
  font_size: number
  tab_size: 2 | 4 | 8
  insert_spaces: boolean
  word_wrap: boolean
  minimap: boolean
  render_whitespace: 'none' | 'boundary' | 'all'
  line_numbers: boolean
  auto_save: boolean
  auto_save_interval: number
  format_on_save: boolean
  bracket_pair_colorization: boolean
  explorer: boolean
}

export const EDITOR_DEFAULTS: EditorPrefs = {
  theme: 'system',
  font_family: 'JetBrains Mono',
  font_size: 13,
  tab_size: 4,
  insert_spaces: true,
  word_wrap: false,
  minimap: true,
  render_whitespace: 'none',
  line_numbers: true,
  auto_save: false,
  auto_save_interval: 30,
  format_on_save: false,
  bracket_pair_colorization: true,
  explorer: true,
}

function coerce<K extends keyof EditorPrefs>(key: K, v: unknown): EditorPrefs[K] {
  const def = EDITOR_DEFAULTS[key]
  if (v === undefined || v === null) return def
  switch (key) {
    case 'theme':
      return (EDITOR_THEMES as readonly string[]).includes(String(v)) ? (v as EditorPrefs[K]) : def
    case 'font_size':
      return (typeof v === 'number' && v >= 10 && v <= 24 ? v : def) as EditorPrefs[K]
    case 'tab_size':
      return (v === 2 || v === 4 || v === 8 ? v : def) as EditorPrefs[K]
    case 'auto_save_interval':
      return (typeof v === 'number' && v >= 2 && v <= 600 ? v : def) as EditorPrefs[K]
    case 'render_whitespace':
      return (['none', 'boundary', 'all'].includes(String(v)) ? v : def) as EditorPrefs[K]
    case 'font_family':
      return (typeof v === 'string' && v.length <= 64 ? v : def) as EditorPrefs[K]
    default:
      return (typeof v === typeof def ? v : def) as EditorPrefs[K]
  }
}

/** Reactive read of the whole editor preference set. */
export function useEditorPrefs(): EditorPrefs {
  return usePrefsStore(
    useShallow((s) => {
      const out = { ...EDITOR_DEFAULTS }
      for (const k of Object.keys(EDITOR_DEFAULTS) as (keyof EditorPrefs)[]) {
        ;(out as Record<string, unknown>)[k] = coerce(k, s.prefs[`editor.${k}`])
      }
      return out
    }),
  )
}

export function setEditorPref<K extends keyof EditorPrefs>(key: K, value: EditorPrefs[K]): void {
  usePrefsStore.getState().set(`editor.${key}`, value)
}
