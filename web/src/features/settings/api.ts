/** Profile API (SPEC §6.3 `me/sessions`, §8.18; Step 19b). */
import { api } from '@/lib/api'

/** Own sessions (SPEC §6.3 `me/sessions`). */
export interface SessionView {
  id: string
  current: boolean
  ip: string
  deviceLabel: string
  createdAt: string
  lastSeenAt: string
}

export const listSessions = (signal?: AbortSignal) =>
  api<{ items: SessionView[] }>('/me/sessions', signal ? { signal } : {})
export const deleteSession = (id: string) =>
  api(`/me/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const revokeOtherSessions = () => api('/me/sessions/revoke-others', { method: 'POST' })

export const sessionsKey = ['me', 'sessions'] as const
