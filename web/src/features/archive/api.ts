import type { Item } from '@/features/browser/api'
import type { ConflictPolicy } from '@/features/file-ops/api'
import type { Task } from '@/features/tasks/api'
import { api } from '@/lib/api'

/** SPEC §8.11 / PROMPT Step 14a: capabilities, compress and extract tasks. */
export type ArchiveFormat =
  | 'zip'
  | '7z'
  | 'rar'
  | 'tar'
  | 'tar.gz'
  | 'tar.bz2'
  | 'tar.xz'
  | 'tar.zst'
  | 'gz'
  | 'bz2'
  | 'xz'
  | 'zst'
  | 'jar'
  | 'war'
  | 'iso'

export interface ArchiveCapability {
  format: ArchiveFormat
  extension: string
  canRead: boolean
  canWrite: boolean
  passwordRead: boolean
  passwordWrite: boolean
  encryptNames: boolean
  splitVolume: boolean
  levels: boolean
  singleFile: boolean
  engine: 'go' | '7z'
  reason?: string
}

export interface ArchiveCapabilities {
  formats: ArchiveCapability[]
  sevenZip: boolean
  sevenZipPath?: string
  passwordStdin: boolean
  maxExtractSize: number
  maxEntries: number
}

export type CompressLevel = 'store' | 'fast' | 'normal' | 'max'
export type NameEncoding =
  'auto' | 'utf-8' | 'gbk' | 'cp437' | 'shift_jis' | 'big5' | 'windows-1252'

export const NAME_ENCODINGS: NameEncoding[] = [
  'auto',
  'utf-8',
  'gbk',
  'cp437',
  'shift_jis',
  'big5',
  'windows-1252',
]

export interface CompressRequest {
  paths: string[]
  format: ArchiveFormat
  name?: string | undefined
  destPath?: string | undefined
  level: CompressLevel
  password?: string | undefined
  encryptNames?: boolean | undefined
  deleteSource?: boolean | undefined
  splitVolumeMb?: number | undefined
}

export interface ExtractRequest {
  path: string
  /** `here`, `new` or a folder path */
  dest: string
  password?: string | undefined
  encoding: NameEncoding
  onConflict: ConflictPolicy
  entries?: string[] | undefined
}

let capsCache: Promise<ArchiveCapabilities> | null = null

/** `GET /archives/capabilities`, cached for the session (tools do not change at runtime). */
export function getArchiveCapabilities(force = false): Promise<ArchiveCapabilities> {
  if (!capsCache || force) {
    capsCache = api<ArchiveCapabilities>('/archives/capabilities').catch((e: unknown) => {
      capsCache = null
      throw e
    })
  }
  return capsCache
}

export function compressItems(req: CompressRequest): Promise<{ task: Task }> {
  return api<{ task: Task }>('/archives/compress', {
    method: 'POST',
    body: {
      paths: req.paths,
      format: req.format,
      name: req.name,
      destPath: req.destPath,
      level: req.level,
      password: req.password,
      encryptNames: req.encryptNames,
      deleteSource: req.deleteSource,
      splitVolumeMb: req.splitVolumeMb,
    },
  })
}

export function extractArchive(req: ExtractRequest): Promise<{ task: Task }> {
  return api<{ task: Task }>('/archives/extract', {
    method: 'POST',
    body: {
      path: req.path,
      dest: req.dest,
      password: req.password,
      encoding: req.encoding,
      onConflict: req.onConflict,
      entries: req.entries,
    },
  })
}

const COMPOUND = ['.tar.gz', '.tar.bz2', '.tar.xz', '.tar.zst', '.tgz', '.tbz2', '.txz', '.tzst']

/** "backup.tar.gz" → "backup" (mirrors the server's Stem). */
export function archiveStem(name: string): string {
  const lower = name.toLowerCase()
  for (const ext of COMPOUND) {
    if (lower.endsWith(ext) && name.length > ext.length) return name.slice(0, -ext.length)
  }
  const i = name.lastIndexOf('.')
  return i > 0 ? name.slice(0, i) : name
}

/** Default archive name (SPEC §8.11): one item → `<name>.<ext>`; several →
 *  `<parent folder>.<ext>`, or `Arsip-<date>.<ext>` at the root. */
export function defaultArchiveName(
  paths: string[],
  extension: string,
  now: Date = new Date(),
): string {
  const base = (p: string) => p.split('/').filter(Boolean).pop() ?? ''
  if (paths.length === 1 && paths[0]) return `${base(paths[0])}.${extension}`
  const first = paths[0] ?? '/'
  const parent = first.slice(0, first.lastIndexOf('/')) || '/'
  if (parent === '/') {
    const d = now.toISOString().slice(0, 10)
    return `Arsip-${d}.${extension}`
  }
  return `${base(parent)}.${extension}`
}

export function isArchiveItem(item: Item | undefined | null): boolean {
  return !!item && !item.isDir && item.category === 'archive'
}
