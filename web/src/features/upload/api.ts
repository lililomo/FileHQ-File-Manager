/** Upload API (SPEC §6.3 "Upload", §6.5 tus). The transfer itself goes
 *  through tus-js-client against TUS_ENDPOINT; only the precheck is JSON. */
import { api, API_BASE } from '@/lib/api'
import { isPublicScope, publicApiPath } from '@/lib/public-scope'

export const TUS_ENDPOINT = `${API_BASE}/upload/tus/`

export type PrecheckStatus = 'ok' | 'conflict' | 'blocked_ext' | 'too_large' | 'name_invalid'

export interface PrecheckFile {
  name: string
  size: number
  relativePath?: string
}

export interface PrecheckExisting {
  size: number
  mtime: string
  isDir: boolean
}

export interface PrecheckItem {
  name: string
  relativePath?: string
  path: string
  status: PrecheckStatus
  reason?: string
  existing?: PrecheckExisting | null
}

export interface PrecheckResult {
  items: PrecheckItem[]
  quotaOk: boolean
  quotaReason?: 'quota' | 'disk'
  needed: number
  free: number
  limit: number
  maxUploadSize: number
  chunkSize: number
}

/** The server checks at most 1000 files per call; the client batches by 200 (SPEC §8.8). */
export const PRECHECK_BATCH = 200

export function precheckUpload(
  path: string,
  files: PrecheckFile[],
  signal?: AbortSignal,
): Promise<PrecheckResult> {
  const opts = { method: 'POST' as const, body: { path, files } }
  const route = isPublicScope() ? publicApiPath('upload/precheck') : '/upload/precheck'
  return api<PrecheckResult>(route, signal ? { ...opts, signal } : opts)
}

/** Key of a planned file inside one batch: relative folder + name. */
export function entryKey(relativePath: string, name: string): string {
  return relativePath ? `${relativePath}/${name}` : name
}
