import { create } from 'zustand'
import { api } from '@/lib/api'
import { useSessionStore } from '@/stores/session'

/** Server-side user preferences (SPEC §8.18): the document from `/auth/me` is
 *  the source of truth; `set()` updates locally at once and sends a debounced
 *  merge patch (`PUT /me/preferences`, 500 ms). Keys must be registered on the
 *  server; unknown keys are rejected with VALIDATION_FAILED. */

export type PrefValue = unknown

export interface PrefsState {
  prefs: Record<string, PrefValue>
  loaded: boolean
  pending: Record<string, PrefValue>
  hydrate: (prefs: Record<string, PrefValue> | undefined) => void
  set: (key: string, value: PrefValue) => void
  /** Send what is pending immediately (used before unload / in tests). */
  flush: () => Promise<void>
}

const DEBOUNCE_MS = 500
let timer: ReturnType<typeof setTimeout> | null = null

async function send(): Promise<void> {
  const { pending } = usePrefsStore.getState()
  if (Object.keys(pending).length === 0) return
  usePrefsStore.setState({ pending: {} })
  try {
    const res = await api<{ prefs: Record<string, PrefValue> }>('/me/preferences', {
      method: 'PUT',
      body: { prefs: pending },
    })
    // keep local changes made meanwhile on top of the server document
    usePrefsStore.setState((s) => ({ prefs: { ...res.prefs, ...s.pending } }))
  } catch {
    // network/validation failure: keep the optimistic local state; the next change retries
    usePrefsStore.setState((s) => ({ pending: { ...pending, ...s.pending } }))
  }
}

export const usePrefsStore = create<PrefsState>((set, get) => ({
  prefs: {},
  loaded: false,
  pending: {},
  hydrate: (prefs) => set({ prefs: { ...(prefs ?? {}) }, loaded: true }),
  set: (key, value) => {
    const cur = get().prefs[key]
    if (JSON.stringify(cur) === JSON.stringify(value)) return
    const prefs = { ...get().prefs }
    if (value === null || value === undefined) delete prefs[key]
    else prefs[key] = value
    set({ prefs, pending: { ...get().pending, [key]: value ?? null } })
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => {
      timer = null
      void send()
    }, DEBOUNCE_MS)
  },
  flush: async () => {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
    await send()
  },
}))

if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', () => {
    const { pending } = usePrefsStore.getState()
    if (Object.keys(pending).length === 0) return
    // best effort on unload; keepalive requests are limited to 64 KB (prefs ≤ 64 KB)
    const csrf = useSessionStore.getState().csrfToken
    void fetch('/api/v1/me/preferences', {
      method: 'PUT',
      credentials: 'same-origin',
      keepalive: true,
      headers: { 'Content-Type': 'application/json', ...(csrf ? { 'X-CSRF-Token': csrf } : {}) },
      body: JSON.stringify({ prefs: pending }),
    })
  })
}

/** Typed read with fallback. */
export function usePref<T>(key: string, fallback: T): T {
  return usePrefsStore((s) => (s.prefs[key] === undefined ? fallback : (s.prefs[key] as T)))
}

export function getPref<T>(key: string, fallback: T): T {
  const v = usePrefsStore.getState().prefs[key]
  return v === undefined ? fallback : (v as T)
}
