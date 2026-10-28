/** Storage analysis (SPEC §8.7 "/storage"). */
import { api, ApiError } from '@/lib/api'
import type { OpResponse } from '@/features/file-ops/api'
import type { Task } from '@/features/tasks/api'

export type NodeKind = 'dir' | 'files' | 'other'

export interface AnalysisNode {
  name: string
  path: string
  kind: NodeKind
  size: number
  files: number
  dirs: number
  children?: AnalysisNode[]
}

export interface AnalysisFile {
  path: string
  name: string
  size: number
  mtime: string
  category: string
}

export type TypeClass = 'image' | 'video' | 'audio' | 'document' | 'archive' | 'other'
export const TYPE_CLASSES: TypeClass[] = ['image', 'video', 'audio', 'document', 'archive', 'other']

export interface StorageAnalysis {
  generatedAt: string
  computedAt: string
  partial: boolean
  total: number
  files: number
  dirs: number
  tree: AnalysisNode
  topFiles: AnalysisFile[]
  byType: Record<string, { bytes: number; files: number }>
  trash?: number
  versions?: number
  quota?: { limit: number; used: number; free: number; percent: number; dirty: boolean }
  cached: boolean
  durationMs: number
}

export const STORAGE_QUERY_KEY = ['storage-analysis'] as const

export function getStorageAnalysis(signal?: AbortSignal): Promise<StorageAnalysis> {
  return api<StorageAnalysis>('/me/storage-analysis', signal ? { signal } : {})
}

export function recomputeStorageAnalysis(): Promise<StorageAnalysis> {
  return api<StorageAnalysis>('/me/storage-analysis', { method: 'POST' })
}

/* Duplicate finder (SPEC §8.7 "Duplikat", Step 23c). */

export interface DuplicateFile {
  path: string
  name: string
  size: number
  mtime: string
  /** The file or one of its folders has a share. */
  shared: boolean
}

export interface DuplicateGroup {
  size: number
  hash: string
  reclaimable: number
  /** paths[0] is the default copy to keep (oldest, shortest path). */
  paths: DuplicateFile[]
}

export interface DuplicateReport {
  taskId: string
  path: string
  groups: DuplicateGroup[]
  reclaimable: number
  scanned: number
  candidates: number
  generatedAt: string
  truncated: boolean
  totalGroups: number
}

export const DUPLICATES_QUERY_KEY = ['duplicates'] as const
export const DUPLICATE_TASK_TYPE = 'duplicate_finder'

/** Latest scan of the last 24 h, or null when there is none (404). */
export async function getDuplicates(signal?: AbortSignal): Promise<DuplicateReport | null> {
  try {
    return await api<DuplicateReport>('/me/duplicates', signal ? { signal } : {})
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return null
    throw e
  }
}

export function startDuplicateScan(path: string): Promise<{ task: Task }> {
  return api('/me/duplicates', { method: 'POST', body: { path } })
}

export function replaceWithSymlinks(
  groups: { keep: string; paths: string[] }[],
): Promise<OpResponse> {
  return api<OpResponse>('/me/duplicates/symlink', { method: 'POST', body: { groups } })
}

/** Drops removed paths from a report (local update after delete/symlink). */
export function withoutPaths(rep: DuplicateReport, removed: Iterable<string>): DuplicateReport {
  const gone = new Set(removed)
  const groups: DuplicateGroup[] = []
  let reclaimable = 0
  for (const g of rep.groups) {
    const paths = g.paths.filter((f) => !gone.has(f.path))
    if (paths.length < 2) continue
    const r = g.size * (paths.length - 1)
    reclaimable += r
    groups.push({ ...g, paths, reclaimable: r })
  }
  return { ...rep, groups, reclaimable }
}
