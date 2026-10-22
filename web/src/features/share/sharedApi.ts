/** Recipient-side share API (SPEC §6.3 `shared-with-me`, `user-suggest`; Step 18b). */
import { api } from '@/lib/api'
import type { SharePermission } from '@/lib/share-scope'

export type SharedStatus = 'active' | 'disabled' | 'expired' | 'broken'

export interface SharedItem {
  shareId: string
  name: string
  isDir: boolean
  permission: SharePermission
  note: string | null
  expiresAt: string | null
  allowReshare: boolean
  receivedAt: string
  status: SharedStatus
  reason?: 'manual' | 'owner_disabled' | 'item_trashed' | 'admin_revoked' | null
}

export interface SharedGroup {
  owner: { username: string; displayName: string }
  items: SharedItem[]
}

/** Header of one received share (the recipient's browser). */
export interface SharedHeader {
  shareId: string
  name: string
  isDir: boolean
  permission: SharePermission
  note: string | null
  expiresAt: string | null
  allowReshare: boolean
  allowDownload: boolean
  owner: { username: string; displayName: string }
}

export interface UserSuggestion {
  username: string
  displayName: string
}

export function listSharedWithMe(signal?: AbortSignal): Promise<{ groups: SharedGroup[] }> {
  return api<{ groups: SharedGroup[] }>('/shared-with-me', signal ? { signal } : {})
}

export function getSharedHeader(shareId: string, signal?: AbortSignal): Promise<SharedHeader> {
  return api<SharedHeader>(
    `/shared-with-me/${encodeURIComponent(shareId)}`,
    signal ? { signal } : {},
  )
}

export function leaveShare(shareId: string): Promise<void> {
  return api<void>(`/shared-with-me/${encodeURIComponent(shareId)}`, { method: 'DELETE' })
}

export function suggestUsers(
  q: string,
  signal?: AbortSignal,
): Promise<{ items: UserSuggestion[] }> {
  return api<{ items: UserSuggestion[] }>(
    '/shares/user-suggest',
    signal ? { query: { q }, signal } : { query: { q } },
  )
}

export const sharedWithMeKey = ['shared-with-me'] as const
export const sharedHeaderKey = (shareId: string) => ['shared-with-me', shareId] as const
