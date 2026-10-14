/** Storage analysis (SPEC §8.7 "/storage"). */
import { api } from '@/lib/api'

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
