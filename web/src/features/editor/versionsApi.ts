/** Versions API client (SPEC §6.3 "Versions", §8.13 "Riwayat versi"; Step 16b). */
import { api, API_BASE } from '@/lib/api'
import type { FileContent } from './api'

export type VersionReason =
  'save' | 'force_save' | 'restore' | 'replace_in_files' | 'external_overwrite'

export interface FileVersion {
  id: string
  path: string
  no: number
  size: number
  sha256: string
  reason: VersionReason
  createdAt: string
}

export interface VersionsResponse {
  items: FileVersion[]
  limit: number
  enabled: boolean
}

export const versionsQueryKey = (path: string) => ['versions', path] as const

export function listVersions(path: string, signal?: AbortSignal): Promise<VersionsResponse> {
  return api<VersionsResponse>('/versions', { query: { path }, ...(signal ? { signal } : {}) })
}

export function getVersionContent(
  id: string,
  encoding?: string,
  signal?: AbortSignal,
): Promise<FileContent> {
  return api<FileContent>(`/versions/${encodeURIComponent(id)}/content`, {
    query: { encoding },
    ...(signal ? { signal } : {}),
  })
}

export interface RestoreResult {
  path: string
  size: number
  etag: string
  /** Pre-image of the replaced content (null when identical / file was gone). */
  version: FileVersion | null
  restored: FileVersion
}

export function restoreVersion(id: string): Promise<RestoreResult> {
  return api<RestoreResult>(`/versions/${encodeURIComponent(id)}/restore`, { method: 'POST' })
}

export function deleteVersion(id: string): Promise<void> {
  return api(`/versions/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function deleteAllVersions(path: string): Promise<void> {
  return api('/versions', { method: 'DELETE', query: { path } })
}

/** Session-bound attachment URL (no signature needed, owner only). */
export function versionDownloadUrl(id: string): string {
  return `${API_BASE}/versions/${encodeURIComponent(id)}/download`
}
