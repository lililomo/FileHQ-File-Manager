/** Admin operations API (SPEC §6.3 "Admin", §8.16–§8.17; Step 19b): audit log
 *  + CSV export, tasks of every user, shares, maintenance jobs and backups. */
import { api } from '@/lib/api'
import type { Task } from '@/features/tasks/api'

export interface AuditEntry {
  id: number
  at: string
  actorUserId: string | null
  actorUsername: string | null
  action: string
  targetType: string | null
  targetId: string | null
  ip: string
  userAgent: string
  details?: Record<string, unknown>
}

export interface AuditFilter {
  actor?: string
  action?: string
  from?: string
  to?: string
  q?: string
}

function auditQuery(f: AuditFilter): Record<string, string | undefined> {
  return {
    actor: f.actor?.trim() || undefined,
    action: f.action?.trim() || undefined,
    from: f.from || undefined,
    to: f.to || undefined,
    q: f.q?.trim() || undefined,
  }
}

export function listAudit(
  f: AuditFilter,
  cursor?: string,
  signal?: AbortSignal,
): Promise<{ items: AuditEntry[]; nextCursor?: string }> {
  return api('/admin/audit', {
    query: { ...auditQuery(f), cursor, limit: 100 },
    ...(signal ? { signal } : {}),
  })
}

/** Same-origin download URL of the streaming CSV export (cookie session). */
export function auditExportUrl(f: AuditFilter): string {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(auditQuery(f))) if (v) params.set(k, v)
  const qs = params.toString()
  return `/api/v1/admin/audit/export.csv${qs ? `?${qs}` : ''}`
}

export interface AdminTask extends Task {
  userId?: string
  username?: string
}

export function listAdminTasks(
  f: { state?: string; user?: string },
  cursor?: string,
  signal?: AbortSignal,
): Promise<{ items: AdminTask[]; nextCursor?: string }> {
  return api('/admin/tasks', {
    query: { state: f.state || undefined, user: f.user?.trim() || undefined, cursor, limit: 50 },
    ...(signal ? { signal } : {}),
  })
}

export const cancelAdminTask = (id: string) =>
  api<{ task: Task }>(`/admin/tasks/${encodeURIComponent(id)}/cancel`, { method: 'POST' })

export type ShareStatus = 'active' | 'disabled' | 'expired' | 'broken'

export interface AdminShare {
  id: string
  ownerId: string
  ownerUsername: string
  path: string
  name: string
  isDir: boolean
  mode: 'public' | 'users'
  hasPassword: boolean
  expiresAt: string | null
  maxDownloads: number | null
  downloadCount: number
  viewCount: number
  recipientCount: number
  allowUpload: boolean
  status: ShareStatus
  disabledReason: string | null
  lastAccessAt: string | null
  createdAt: string
}

export function listAdminShares(
  f: { mode?: string; status?: string; owner?: string },
  cursor?: string,
  signal?: AbortSignal,
): Promise<{ items: AdminShare[]; nextCursor: string | null }> {
  return api('/admin/shares', {
    query: {
      mode: f.mode || undefined,
      status: f.status || undefined,
      owner: f.owner?.trim() || undefined,
      cursor,
    },
    ...(signal ? { signal } : {}),
  })
}

export const revokeAdminShare = (id: string) =>
  api(`/admin/shares/${encodeURIComponent(id)}/revoke`, { method: 'POST' })

export interface JobSchedule {
  name: string
  schedule: string
  timeZone: string
  nextRun: string
  lastRun?: string
  lastDurationMs?: number
  lastError?: string
  running: boolean
}

export interface MaintenanceJob {
  name: string
  slug?: string
  manual: boolean
  schedule?: JobSchedule
  lastTask?: Task
}

export interface Backup {
  name: string
  size: number
  createdAt: string
  label?: string
}

export interface MaintenanceView {
  timeZone: string
  jobs: MaintenanceJob[]
  backups: Backup[]
}

export const getMaintenance = (signal?: AbortSignal) =>
  api<MaintenanceView>('/admin/maintenance', signal ? { signal } : {})

export interface RunInput {
  userId?: string
  force?: boolean
  confirm?: string
  prune?: boolean
  label?: string
}

export const runMaintenance = (slug: string, body: RunInput = {}) =>
  api<{ task: Task }>(`/admin/maintenance/${encodeURIComponent(slug)}`, {
    method: 'POST',
    body,
  })

export const backupUrl = (name: string) =>
  `/api/v1/admin/maintenance/backups/${encodeURIComponent(name)}`

export interface IntegrityReport {
  checkedAt: string
  orphanFolders: { name: string; isDir: boolean }[]
  missingHomes: { userId: string; username: string; provisioned: boolean }[]
  brokenRefs: { table: string; userId: string; username: string; path: string }[]
  orphanSystemDirs: { area: string; name: string }[]
  counts: Record<string, number>
  truncated: boolean
}

export const opsKeys = {
  audit: ['admin', 'audit'] as const,
  tasks: ['admin', 'tasks'] as const,
  shares: ['admin', 'shares'] as const,
  maintenance: ['admin', 'maintenance'] as const,
}
