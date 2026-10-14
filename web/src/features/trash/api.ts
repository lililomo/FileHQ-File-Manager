/** Recycle Bin API client (SPEC §6.3 "Trash", §8.6; Step 10a). */
import type { ConflictPolicy } from '@/features/file-ops/api'
import { api, type Query } from '@/lib/api'

export type TrashKind =
  'file' | 'folder' | 'image' | 'document' | 'video' | 'audio' | 'archive' | 'other'
export const TRASH_KINDS: TrashKind[] = [
  'file',
  'folder',
  'image',
  'document',
  'video',
  'audio',
  'archive',
  'other',
]

export type TrashSort = 'deleted' | 'name' | 'size' | 'expires' | 'path'

export interface TrashItem {
  id: string
  name: string
  originalPath: string
  isDir: boolean
  size: number
  itemCount: number
  kind: TrashKind
  deletedAt: string
  expiresAt: string | null
  batchId?: string
}

export interface TrashListResponse {
  items: TrashItem[]
  total: number
  totalBytes: number
  nextCursor: string | null
  retentionDays: number
}

export interface TrashListOptions {
  kind?: TrashKind | ''
  q?: string
  sort?: TrashSort
  dir?: 'asc' | 'desc'
  cursor?: string | null
  limit?: number
}

export function trashQueryKey(opts: TrashListOptions) {
  return ['trash', opts.kind ?? '', opts.q ?? '', opts.sort ?? 'deleted', opts.dir ?? ''] as const
}

export function listTrash(
  opts: TrashListOptions = {},
  signal?: AbortSignal,
): Promise<TrashListResponse> {
  const query: Query = {
    kind: opts.kind || undefined,
    q: opts.q || undefined,
    sort: opts.sort,
    dir: opts.dir,
    cursor: opts.cursor ?? undefined,
    limit: opts.limit,
  }
  return api<TrashListResponse>('/trash', signal ? { query, signal } : { query })
}

export interface RestoreResult {
  id: string
  path: string
  to: string
  status: 'ok' | 'renamed' | 'overwritten' | 'skipped'
  isDir: boolean
  size: number
  overwrittenId?: string
}

export interface RestoreResponse {
  results: RestoreResult[]
  reactivateShares: string[]
  undo: null
}

export interface RestoreOptions {
  /** Destination folder; omitted = each item's original location. */
  to?: string
  onConflict?: ConflictPolicy
  /** Keyed by trash id (the `source` of a restore conflict). */
  perItem?: Record<string, ConflictPolicy>
}

export function restoreTrash(ids: string[], opts: RestoreOptions = {}): Promise<RestoreResponse> {
  const body: Record<string, unknown> = { ids }
  if (opts.to) body.to = opts.to
  if (opts.onConflict) body.onConflict = opts.onConflict
  if (opts.perItem && Object.keys(opts.perItem).length > 0) body.perItem = opts.perItem
  return api<RestoreResponse>('/trash/restore', { method: 'POST', body })
}

export interface PurgeResponse {
  results: { id: string; path?: string; size?: number; isDir?: boolean; status: 'ok' | 'gone' }[]
  deleted: number
  bytesFreed: number
  undo: null
}

export function purgeTrash(ids: string[]): Promise<PurgeResponse> {
  return api<PurgeResponse>('/trash/purge', { method: 'POST', body: { ids } })
}

export function emptyTrash(): Promise<PurgeResponse> {
  return api<PurgeResponse>('/trash/empty', { method: 'POST', body: { confirm: 'HAPUS' } })
}
