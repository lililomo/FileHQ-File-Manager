import { ApiError } from '@/lib/api'
import { usePrefsStore } from '@/stores/prefs'
import { useSessionStore, type User } from '@/stores/session'
import { useUIStore } from '@/stores/ui'
import { isLanguage, setLanguage } from '@/i18n'
import { fetchCsrf, fetchMe } from './api'

let inflight: Promise<User | null> | null = null

/** Apply server-side preferences that affect the shell (theme, accent, language). */
export function applyUserPrefs(user: User): void {
  usePrefsStore.getState().hydrate(user.prefs)
  useUIStore.getState().applyServerPrefs(user.prefs)
  const lang = user.prefs?.['ui.language'] ?? user.language
  if (isLanguage(lang)) void setLanguage(lang, false)
}

/** Resolve the current session once (deduplicated); null when anonymous. */
export async function ensureSession(force = false): Promise<User | null> {
  const state = useSessionStore.getState()
  if (!force) {
    if (state.status === 'authenticated') return state.user
    if (state.status === 'anonymous') return null
  }
  if (!inflight) {
    inflight = (async () => {
      try {
        const me = await fetchMe()
        let csrf = useSessionStore.getState().csrfToken
        if (!csrf) csrf = (await fetchCsrf()).csrfToken
        useSessionStore.getState().setAuthenticated(me.user, me.config, csrf)
        applyUserPrefs(me.user)
        return me.user
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          useSessionStore.getState().setAnonymous()
          return null
        }
        throw e
      } finally {
        inflight = null
      }
    })()
  }
  return inflight
}
