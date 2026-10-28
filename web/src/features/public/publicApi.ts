/** Guest API behind `/s/<token>` (SPEC §6.6, Step 18a). No session: the
 *  token/slug in the path plus the unlock cookie authorise every call, so
 *  401/403 here never redirect to the login page. */
import type { ListResponse } from '@/features/browser/api'
import { api } from '@/lib/api'

export interface PublicMeta {
  name?: string
  isDir: boolean
  size?: number
  mime?: string | null
  category?: string
  mtime?: string
  itemCount?: number | null
  owner?: string
  note?: string | null
  allowDownload: boolean
  allowUpload: boolean
  hideListing: boolean
  expiresAt: string | null
  passwordRequired: boolean
  unlocked: boolean
  downloadLimitReached: boolean
  maxDownloads?: number | null
  downloadCount: number
  /** Step 23e: content origin; when set, content URLs come from `POST …/sign` */
  contentBaseUrl?: string
}

const base = (token: string) => `public/shares/${encodeURIComponent(token)}`

export function getPublicMeta(token: string, signal?: AbortSignal): Promise<PublicMeta> {
  return api<PublicMeta>(base(token), signal ? { signal, skipAuthRedirect: true } : { skipAuthRedirect: true })
}

export function unlockPublic(token: string, password: string): Promise<void> {
  return api<void>(`${base(token)}/unlock`, { method: 'POST', body: { password }, skipAuthRedirect: true })
}

export type PublicSortField = 'name' | 'size' | 'mtime'

export function listPublic(
  token: string,
  path: string,
  sort: PublicSortField,
  dir: 'asc' | 'desc',
  cursor: string | null,
  signal?: AbortSignal,
): Promise<ListResponse> {
  const query = { path, sort, dir, cursor: cursor ?? undefined }
  return api<ListResponse>(`${base(token)}/list`, signal ? { query, signal, skipAuthRedirect: true } : { query, skipAuthRedirect: true })
}

export function publicZip(token: string, paths: string[]): Promise<{ url: string; name: string; count: number }> {
  return api<{ url: string; name: string; count: number }>(`${base(token)}/zip`, {
    method: 'POST',
    body: { paths },
    skipAuthRedirect: true,
  })
}

export type PublicSignPurpose = 'media' | 'inline' | 'download' | 'thumb'

/** `POST …/sign` (Step 23e): URLs signed with the share scope for the content
 *  origin, where the unlock cookie does not exist. */
export function signPublicUrl(
  token: string,
  path: string,
  purpose: PublicSignPurpose,
  size?: number,
): Promise<{ url: string; expiresAt: string; purpose: PublicSignPurpose }> {
  return api(`${base(token)}/sign`, {
    method: 'POST',
    body: size ? { path, purpose, size } : { path, purpose },
    skipAuthRedirect: true,
  })
}

export function signPublicUrls(
  token: string,
  items: { path: string; purpose: PublicSignPurpose; size?: number }[],
): Promise<{ urls: { path: string; url: string }[]; expiresAt: string; invalid: string[] }> {
  return api(`${base(token)}/sign`, { method: 'POST', body: { items }, skipAuthRedirect: true })
}

export const publicMetaKey = (token: string) => ['public', token, 'meta'] as const
export const publicListKey = (token: string, path: string, sort: PublicSortField, dir: 'asc' | 'desc') =>
  ['public', token, 'list', path, sort, dir] as const
