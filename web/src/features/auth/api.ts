import { api } from '@/lib/api'
import type { ClientConfig, User } from '@/stores/session'

export interface LoginResponse {
  user: User
  mustChangePassword: boolean
  csrfToken: string
}
export interface MeResponse {
  user: User
  config: ClientConfig
}

export const login = (username: string, password: string) =>
  api<LoginResponse>('/auth/login', {
    method: 'POST',
    body: { username, password },
    skipAuthRedirect: true,
  })

export const logout = () => api<void>('/auth/logout', { method: 'POST', skipAuthRedirect: true })

export const fetchMe = () => api<MeResponse>('/auth/me', { skipAuthRedirect: true })

export const fetchCsrf = () => api<{ csrfToken: string }>('/auth/csrf', { skipAuthRedirect: true })

export const changePassword = (currentPassword: string, newPassword: string) =>
  api<void>('/auth/change-password', {
    method: 'POST',
    body: { currentPassword, newPassword },
    skipPasswordChangeRedirect: true,
  })
