/** File-operation API client (SPEC §6.3 "Files", §8.5; Step 9a contract).
 *  Requests use camelCase; responses are `{results, item?, batchId?,
 *  undoToken?, undo, warnings?}`. */
import type { Item } from '@/features/browser/api'
import { api, ApiError } from '@/lib/api'

export type ConflictPolicy = 'fail' | 'overwrite' | 'skip' | 'rename'
export type TransferOp = 'copy' | 'move'

export interface ItemResult {
  path: string
  to?: string
  status: 'ok' | 'skipped' | 'overwritten' | 'renamed' | 'merged'
  isDir: boolean
  trashId?: string
  warnings?: string[]
}

export interface UndoInfo {
  kind: 'rename' | 'move' | 'restore' | 'delete_created' | 'batch_rename'
  path?: string
  newName?: string
  items?: { from: string; to: string }[]
  created?: string[]
  batchId?: string
}

export interface OpResponse {
  results: ItemResult[]
  item?: Item
  batchId?: string
  undoToken?: string
  undo: UndoInfo | null
  warnings?: string[]
}

export interface ConflictStat {
  size: number
  mtime: string
  isDir: boolean
}

export interface Conflict {
  /** destination path */
  path: string
  /** source item (key of `perItem`) */
  source: string
  existing: ConflictStat
  incoming: ConflictStat
}

/** `details.conflicts[]` of a 409 ALREADY_EXISTS, or null for other errors. */
export function conflictsOf(e: unknown): Conflict[] | null {
  if (!(e instanceof ApiError) || e.code !== 'ALREADY_EXISTS') return null
  const raw = e.details.conflicts
  if (!Array.isArray(raw)) return null
  const out: Conflict[] = []
  for (const c of raw as Record<string, unknown>[]) {
    if (typeof c.path !== 'string' || typeof c.source !== 'string') continue
    out.push({
      path: c.path,
      source: c.source,
      existing: statOf(c.existing),
      incoming: statOf(c.incoming),
    })
  }
  return out
}

function statOf(v: unknown): ConflictStat {
  const o = (v && typeof v === 'object' ? v : {}) as Record<string, unknown>
  return {
    size: typeof o.size === 'number' ? o.size : 0,
    mtime: typeof o.mtime === 'string' ? o.mtime : '',
    isDir: o.isDir === true,
  }
}

/** True for `413 PAYLOAD_TOO_LARGE details.reason=task_required` (task engine, Step 11). */
export function isTaskRequired(e: unknown): boolean {
  return (
    e instanceof ApiError && e.code === 'PAYLOAD_TOO_LARGE' && e.details.reason === 'task_required'
  )
}

export function renameItem(path: string, newName: string): Promise<OpResponse> {
  return api<OpResponse>('/files/rename', { method: 'POST', body: { path, newName } })
}

export interface TransferPolicy {
  onConflict?: ConflictPolicy
  perItem?: Record<string, ConflictPolicy>
}

export function transferItems(
  op: TransferOp,
  paths: string[],
  to: string,
  policy: TransferPolicy = {},
): Promise<OpResponse> {
  const body: Record<string, unknown> = { paths, to }
  if (policy.onConflict) body.onConflict = policy.onConflict
  if (policy.perItem && Object.keys(policy.perItem).length > 0) body.perItem = policy.perItem
  return api<OpResponse>(`/files/${op}`, { method: 'POST', body })
}

export function duplicateItem(path: string): Promise<OpResponse> {
  return api<OpResponse>('/files/duplicate', { method: 'POST', body: { path } })
}

/** `target` is relative to the link's folder (SPEC §5.4). */
export function createSymlink(path: string, target: string): Promise<OpResponse> {
  return api<OpResponse>('/files/symlink', { method: 'POST', body: { path, target } })
}

export interface ChmodInput {
  paths: string[]
  /** octal like "0644" */
  mode: string
  recursive?: boolean
  dirMode?: string
}

export function chmodItems(input: ChmodInput): Promise<OpResponse> {
  const body: Record<string, unknown> = { paths: input.paths, mode: input.mode }
  if (input.recursive) body.recursive = true
  if (input.dirMode) body.dirMode = input.dirMode
  return api<OpResponse>('/files/chmod', { method: 'POST', body })
}

export function deleteItems(paths: string[]): Promise<OpResponse> {
  return api<OpResponse>('/files/delete', { method: 'POST', body: { paths } })
}

export interface RenameRule {
  find?: string
  replace?: string
  regex?: boolean
  case_insensitive?: boolean
  numbering?: { pattern: string; start: number; step: number; pad: number }
  case?: 'upper' | 'lower' | 'title' | 'none'
  ext_mode?: 'keep' | 'include'
}

export interface PreviewItem {
  from: string
  to: string
  status: 'ok' | 'collision' | 'invalid'
  reason?: string
}

export interface PreviewResponse {
  items: PreviewItem[]
  clean: boolean
}

export function batchRenamePreview(paths: string[], rule: RenameRule): Promise<PreviewResponse> {
  return api<PreviewResponse>('/files/batch-rename/preview', {
    method: 'POST',
    body: { paths, rule },
  })
}

export function batchRenameApply(
  paths: string[],
  rule: RenameRule,
): Promise<OpResponse & { items: PreviewItem[] }> {
  return api<OpResponse & { items: PreviewItem[] }>('/files/batch-rename', {
    method: 'POST',
    body: { paths, rule },
  })
}

export function undoOperation(token: string): Promise<OpResponse> {
  return api<OpResponse>('/files/undo', { method: 'POST', body: { token } })
}

export function listTemplates(signal?: AbortSignal): Promise<string[]> {
  return api<{ templates: string[] }>('/files/templates', signal ? { signal } : {}).then(
    (r) => r.templates,
  )
}
