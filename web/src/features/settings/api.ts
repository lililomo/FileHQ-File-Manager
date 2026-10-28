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

/** TOTP 2FA (SPEC §6.3 `me/totp/*`, Step 23a). */
export interface TotpSetup {
  secret: string
  otpauthUri: string
}
export const setupTotp = () => api<TotpSetup>('/me/totp/setup', { method: 'POST', body: {} })
export const enableTotp = (code: string) =>
  api<{ recoveryCodes: string[]; enabledAt: string }>('/me/totp/enable', {
    method: 'POST',
    body: { code },
  })
export const disableTotp = (password: string, code: string) =>
  api<void>('/me/totp/disable', { method: 'POST', body: { password, code } })
export const regenerateRecoveryCodes = (password: string) =>
  api<{ recoveryCodes: string[] }>('/me/totp/recovery-codes', {
    method: 'POST',
    body: { password },
  })
