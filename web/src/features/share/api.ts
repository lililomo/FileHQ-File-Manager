/** Owner-side share API (SPEC §6.3 "Shares", Step 18a: public links). */
import { api } from '@/lib/api'

export type ShareMode = 'public' | 'users'
export type ShareStatus = 'active' | 'disabled' | 'expired' | 'broken'
export type ShareDisabledReason = 'manual' | 'owner_disabled' | 'item_trashed' | 'admin_revoked'

export interface Share {
  id: string
  path: string
  name: string
  isDir: boolean
  mode: ShareMode
  slug: string | null
  hasPassword: boolean
  expiresAt: string | null
  maxDownloads: number | null
  downloadCount: number
  viewCount: number
  allowDownload: boolean
  allowUpload: boolean
  hideOwner: boolean
  hideListing: boolean
  note: string | null
  disabledAt: string | null
  disabledReason: ShareDisabledReason | null
  brokenAt: string | null
  lastAccessAt: string | null
  createdAt: string
  updatedAt: string
  /** the public link; empty when the stored token cannot be decrypted any more */
  url: string
  status: ShareStatus
  /** mode users (Step 18b) */
  allowReshare: boolean
  recipients?: ShareRecipient[]
}

export interface ShareRecipient {
  username: string
  displayName: string
  permission: 'viewer' | 'editor'
  note: string | null
  createdAt: string
  inactive?: boolean
}

export interface ShareRecipientInput {
  username: string
  permission?: 'viewer' | 'editor'
  note?: string
}

/** Create/update payload. `null` removes a value (password, slug, note, expiry). */
export interface ShareInput {
  path?: string
  mode?: ShareMode
  slug?: string | null
  password?: string | null
  expiresAt?: string | null
  expiresInDays?: number | null
  maxDownloads?: number | null
  allowDownload?: boolean
  allowUpload?: boolean
  hideOwner?: boolean
  hideListing?: boolean
  note?: string | null
  disabled?: boolean
  /** mode users (Step 18b): the full set on PATCH */
  recipients?: ShareRecipientInput[]
  allowReshare?: boolean
}

export interface SharePage {
  items: Share[]
  nextCursor: string | null
}

export interface ShareAccessLogEntry {
  id: number
  at: string
  ipTrunc: string
  action: 'view' | 'list' | 'download' | 'zip' | 'unlock_ok' | 'unlock_fail' | 'upload' | 'denied'
  path: string | null
  userId: string | null
  userAgent: string
}

export interface ShareAccessLogPage {
  items: ShareAccessLogEntry[]
  nextCursor: string | null
}

export function createShare(input: ShareInput): Promise<{ share: Share; url: string }> {
  return api<{ share: Share; url: string }>('/shares', { method: 'POST', body: input })
}

export function listShares(
  opts: { cursor?: string; mode?: ShareMode; status?: ShareStatus } = {},
): Promise<SharePage> {
  return api<SharePage>('/shares', {
    query: { cursor: opts.cursor, mode: opts.mode, status: opts.status },
  })
}

export function sharesByPath(path: string, signal?: AbortSignal): Promise<{ items: Share[] }> {
  return api<{ items: Share[] }>(
    '/shares/by-path',
    signal ? { query: { path }, signal } : { query: { path } },
  )
}

export function getShare(id: string): Promise<{ share: Share }> {
  return api<{ share: Share }>(`/shares/${encodeURIComponent(id)}`)
}

export function updateShare(id: string, input: ShareInput): Promise<{ share: Share }> {
  return api<{ share: Share }>(`/shares/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
  })
}

export function deleteShare(id: string): Promise<void> {
  return api<void>(`/shares/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function disableShare(id: string): Promise<{ share: Share }> {
  return api<{ share: Share }>(`/shares/${encodeURIComponent(id)}/disable`, { method: 'POST' })
}

export function enableShare(id: string): Promise<{ share: Share }> {
  return api<{ share: Share }>(`/shares/${encodeURIComponent(id)}/enable`, { method: 'POST' })
}

export function regenerateShareToken(id: string): Promise<{ share: Share; url: string }> {
  return api<{ share: Share; url: string }>(`/shares/${encodeURIComponent(id)}/regenerate-token`, {
    method: 'POST',
  })
}

export function shareAccessLogs(id: string, cursor?: string): Promise<ShareAccessLogPage> {
  return api<ShareAccessLogPage>(`/shares/${encodeURIComponent(id)}/access-logs`, {
    query: { cursor },
  })
}

/** Query key of the shares of one item (dialog, badge refresh). */
export const sharesByPathKey = (path: string) => ['shares', 'by-path', path] as const
