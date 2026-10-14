/** User activity journal (SPEC §8.16 "Aktivitas user"). */
import { api, type Query } from '@/lib/api'

export interface ActivityEntry {
  id: number
  at: string
  action: string
  path: string | null
  path2: string | null
  details: Record<string, unknown>
  batchId: string | null
  exists: boolean
}

export interface ActivityResponse {
  items: ActivityEntry[]
  nextCursor: string | null
  retentionDays: number
}

export interface ActivityFilter {
  action?: string
  from?: string
  to?: string
  q?: string
  limit?: number
}

/** Actions a user can filter by (SPEC §8.16 list, those written so far). */
export const ACTIVITY_ACTIONS = [
  'open',
  'create',
  'rename',
  'move',
  'copy',
  'duplicate',
  'symlink',
  'chmod',
  'delete',
  'delete_permanent',
  'restore',
  'purge',
  'purge_auto',
  'trash_empty',
  'batch_rename',
  'undo',
  'dir_size',
  'checksum',
] as const

export function activityQueryKey(f: ActivityFilter) {
  return ['activity', f.action ?? '', f.from ?? '', f.to ?? '', f.q ?? ''] as const
}

export function listActivity(
  f: ActivityFilter,
  cursor: string | null,
  signal?: AbortSignal,
): Promise<ActivityResponse> {
  const query: Query = {
    action: f.action || undefined,
    from: f.from || undefined,
    to: f.to || undefined,
    q: f.q || undefined,
    limit: f.limit,
    cursor: cursor ?? undefined,
  }
  return api<ActivityResponse>('/me/activity', signal ? { query, signal } : { query })
}

export function clearActivity(): Promise<{ deleted: number }> {
  return api<{ deleted: number }>('/me/activity', { method: 'DELETE' })
}
