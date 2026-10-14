/** In-app notifications API (SPEC §8.16). */
import { api } from '@/lib/api'

export type NotificationType =
  'task_done' | 'task_failed' | 'share_received' | 'share_upload' | 'system'

export interface Notification {
  id: string
  type: NotificationType | string
  title: string
  body?: string
  link?: string
  readAt: string | null
  createdAt: string
}

export interface NotificationPage {
  items: Notification[]
  nextCursor?: string
  unread: number
}

export function listNotifications(
  opts: { unread?: boolean; cursor?: string; limit?: number } = {},
  signal?: AbortSignal,
): Promise<NotificationPage> {
  const query = { unread: opts.unread ? 1 : undefined, cursor: opts.cursor, limit: opts.limit }
  return api('/notifications', signal ? { query, signal } : { query })
}

export function markNotificationsRead(
  ids: string[] | 'all',
): Promise<{ read: number; unread: number }> {
  return api('/notifications/read', {
    method: 'POST',
    body: ids === 'all' ? { all: true } : { ids },
  })
}

export function deleteNotification(id: string): Promise<void> {
  return api(`/notifications/${encodeURIComponent(id)}`, { method: 'DELETE' })
}
