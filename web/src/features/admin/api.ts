/** Admin API (SPEC §6.3 "Admin", §8.17; Step 19a): dashboard, users, settings, branding. */
import { api } from '@/lib/api'
import type { Role } from '@/stores/session'

export type UserStatus = 'active' | 'disabled'

export interface AdminUser {
  id: string
  username: string
  displayName: string
  role: Role
  status: UserStatus
  quota: { limit: number; used: number; trash: number; versions: number; dirty: boolean }
  lastLoginAt: string | null
  homeProvisioned: boolean
  homeProvisionedAt: string | null
  homeWarning: string | null
  createdAt: string
  updatedAt: string
  mustChangePassword: boolean
  totpEnabled: boolean
  lockedUntil: string | null
  createdBy: string | null
}

export interface AdminUserPage {
  items: AdminUser[]
  nextCursor: string | null
  total: number
}

export interface UserListFilter {
  q?: string
  role?: Role | ''
  status?: UserStatus | ''
  cursor?: string
  limit?: number
}

export function listUsers(f: UserListFilter, signal?: AbortSignal): Promise<AdminUserPage> {
  return api<AdminUserPage>('/admin/users', {
    query: {
      q: f.q || undefined,
      role: f.role || undefined,
      status: f.status || undefined,
      cursor: f.cursor,
      limit: f.limit,
    },
    ...(signal ? { signal } : {}),
  })
}

export interface CreateUserInput {
  username: string
  displayName?: string
  role: Role
  quotaBytes?: number
  temporaryPassword?: string
}

export interface CreateUserResult {
  user: AdminUser
  temporaryPassword: string
  warnings: string[]
}

export const createUser = (input: CreateUserInput) =>
  api<CreateUserResult>('/admin/users', { method: 'POST', body: input })

export interface UpdateUserInput {
  displayName?: string
  role?: Role
  quotaBytes?: number
  status?: UserStatus
}

export const updateUser = (id: string, input: UpdateUserInput) =>
  api<{ user: AdminUser }>(`/admin/users/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
  })

export const resetPassword = (id: string) =>
  api<{ temporaryPassword: string }>(`/admin/users/${encodeURIComponent(id)}/reset-password`, {
    method: 'POST',
    body: {},
  })

/** Clears the user's TOTP secret + recovery codes and revokes their sessions (Step 23a). */
export const resetTotp = (id: string) =>
  api<void>(`/admin/users/${encodeURIComponent(id)}/reset-totp`, {
    method: 'POST',
    body: {},
  })

export const revokeSessions = (id: string) =>
  api<void>(`/admin/users/${encodeURIComponent(id)}/revoke-sessions`, {
    method: 'POST',
    body: {},
  })

export const setUserStatus = (id: string, status: UserStatus) =>
  api<{ user: AdminUser }>(
    `/admin/users/${encodeURIComponent(id)}/${status === 'disabled' ? 'disable' : 'enable'}`,
    { method: 'POST', body: {} },
  )

export type DeleteMode = 'purge' | 'archive'

export const deleteUser = (id: string, confirmUsername: string, mode: DeleteMode) =>
  api<{ task: { id: string; state: string } }>(`/admin/users/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    body: { confirmUsername, mode },
  })

// --- dashboard --------------------------------------------------------------

export interface DashboardWarning {
  code:
    | 'initial_password_in_env'
    | 'cross_fs_data_dir'
    | 'swap_low'
    | 'disk_low'
    | 'quota_over_disk'
    | 'dependency_missing'
    | 'sevenzip_password_argv'
    | 'admin_browse_enabled'
    | string
  details?: Record<string, unknown>
}

export interface Dashboard {
  users: { active: number; disabled: number }
  storage: {
    usedBytes: number
    quotaSumBytes: number
    disk: { total: number; free: number; used: number }
  }
  topUsers: { id: string; username: string; displayName: string; used: number; quota: number }[]
  tasks: { running: number; queued: number }
  publicShares: number
  failedLogins24h: number
  version: { version: string; commit: string; buildDate: string; go: string }
  deps: {
    name: string
    env: string
    path: string
    ok: boolean
    version?: string
    feature: string
    optional: boolean
  }[]
  warnings: DashboardWarning[]
  generatedAt: string
}

export const getDashboard = (signal?: AbortSignal) =>
  api<Dashboard>('/admin/dashboard', signal ? { signal } : {})

// --- settings ---------------------------------------------------------------

export type SettingKind = 'size' | 'int' | 'bool' | 'duration' | 'enum' | 'list' | 'string' | 'tz'
export type SettingGroup =
  'upload' | 'storage' | 'trash' | 'remote' | 'sharing' | 'security' | 'appearance' | 'tasks'
export type SettingSource = 'default' | 'env' | 'override'
export type SettingValue = string | number | boolean | string[]

export interface AdminSetting {
  key: string
  group: SettingGroup
  type: SettingKind
  value: SettingValue
  default: SettingValue
  envValue?: SettingValue
  source: SettingSource
  readOnly?: boolean
  validation?: { min?: number; max?: number; maxLen?: number; options?: string[] }
  updatedAt?: string
  updatedBy?: string
}

export const listSettings = (signal?: AbortSignal) =>
  api<{ items: AdminSetting[] }>('/admin/settings', signal ? { signal } : {})

export const putSettings = (values: Record<string, SettingValue>) =>
  api<{ items: AdminSetting[] }>('/admin/settings', { method: 'PUT', body: values })

export const resetSettings = (keys: string[]) =>
  api<{ items: AdminSetting[] }>('/admin/settings/reset', { method: 'POST', body: { keys } })

export async function uploadLogo(file: File): Promise<{ logoUrl: string }> {
  const form = new FormData()
  form.append('logo', file)
  return api<{ logoUrl: string }>('/admin/branding/logo', { method: 'POST', body: form })
}

export const deleteLogo = () => api<void>('/admin/branding/logo', { method: 'DELETE' })

export const adminKeys = {
  dashboard: ['admin', 'dashboard'] as const,
  users: ['admin', 'users'] as const,
  settings: ['admin', 'settings'] as const,
}
