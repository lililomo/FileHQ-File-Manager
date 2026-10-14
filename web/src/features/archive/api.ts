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

// ---- Archive browser (SPEC §8.11 "Archive browser", PROMPT Step 14b) ----

export interface ArchiveEntry {
  /** inner path (no leading slash) */
  path: string
  name: string
  isDir: boolean
  isSymlink?: boolean
  size: number
  /** -1 when the container does not record it */
  compressedSize: number
  mtime: string
  encrypted: boolean
  method?: string
  implicit?: boolean
  itemCount: number
}

export interface ArchiveEntries {
  archive: string
  inner: string
  format: ArchiveFormat
  items: ArchiveEntry[]
  nextCursor?: string
  total: number
  totalEntries: number
  size: number
  encrypted: boolean
  encoding?: string
  supportsModify: boolean
  modifyReason?: 'format' | 'encrypted' | 'too_large_to_modify'
  /** present when a password was accepted: id for signed entry URLs */
  session?: string
  unsafe?: number
}

export interface EntriesQuery {
  path: string
  inner?: string
  encoding?: NameEncoding
  cursor?: string
  limit?: number
  /** sent in the X-Archive-Password header, never a query string */
  password?: string
}

export const ARCHIVE_PASSWORD_HEADER = 'X-Archive-Password'

export function archiveEntriesKey(q: EntriesQuery): unknown[] {
  return [
    'archive-entries',
    q.path,
    q.inner ?? '',
    q.encoding ?? 'auto',
    q.cursor ?? '',
    q.password ? 'pw' : '',
  ]
}

/** `GET /archives/entries`; a 401 here is ARCHIVE_PASSWORD_REQUIRED (not a
 *  lost session), so the auth redirect is skipped and the caller inspects
 *  the code. */
export function listArchiveEntries(q: EntriesQuery, signal?: AbortSignal): Promise<ArchiveEntries> {
  const query: Record<string, string | number> = { path: q.path }
  if (q.inner) query.inner = q.inner
  if (q.encoding && q.encoding !== 'auto') query.encoding = q.encoding
  if (q.cursor) query.cursor = q.cursor
  if (q.limit) query.limit = q.limit
  return api<ArchiveEntries>('/archives/entries', {
    query,
    ...(q.password ? { headers: { [ARCHIVE_PASSWORD_HEADER]: q.password } } : {}),
    ...(signal ? { signal } : {}),
    skipAuthRedirect: true,
  })
}

export interface SignedEntry {
  url: string
  expiresAt: string
  purpose: 'entry'
  path: string
  inner: string
}

/** Signed URL of one member (`POST /archives/sign`); the archive session id
 *  rides along so an encrypted member can be decrypted without resending
 *  the password. */
export function signArchiveEntry(
  path: string,
  inner: string,
  session?: string,
): Promise<SignedEntry> {
  return api<SignedEntry>('/archives/sign', {
    method: 'POST',
    body: { path, inner, session: session || undefined },
  })
}

export interface ExtractEntriesRequest {
  path: string
  entries: string[]
  /** `here`, `new` or a folder path */
  dest: string
  password?: string | undefined
  encoding?: NameEncoding | undefined
  onConflict: ConflictPolicy
}

export function extractArchiveEntries(req: ExtractEntriesRequest): Promise<{ task: Task }> {
  return api<{ task: Task }>('/archives/extract-entries', {
    method: 'POST',
    body: {
      path: req.path,
      entries: req.entries,
      dest: req.dest,
      password: req.password,
      encoding: req.encoding,
      onConflict: req.onConflict,
    },
  })
}

export function addToArchive(
  path: string,
  sources: string[],
  innerDir: string,
): Promise<{ task: Task }> {
  return api<{ task: Task }>('/archives/add', {
    method: 'POST',
    body: { path, sources, innerDir: innerDir || undefined },
  })
}

export function removeFromArchive(path: string, entries: string[]): Promise<{ task: Task }> {
  return api<{ task: Task }>('/archives/remove-entries', {
    method: 'POST',
    body: { path, entries },
  })
}

const EXT_CATEGORY: Record<string, string> = {
  jpg: 'image',
  jpeg: 'image',
  png: 'image',
  gif: 'image',
  webp: 'image',
  bmp: 'image',
  svg: 'image',
  avif: 'image',
  ico: 'image',
  tif: 'image',
  tiff: 'image',
  heic: 'image',
  mp4: 'video',
  webm: 'video',
  mkv: 'video',
  mov: 'video',
  avi: 'video',
  m4v: 'video',
  mp3: 'audio',
  m4a: 'audio',
  aac: 'audio',
  ogg: 'audio',
  opus: 'audio',
  wav: 'audio',
  flac: 'audio',
  zip: 'archive',
  '7z': 'archive',
  rar: 'archive',
  tar: 'archive',
  gz: 'archive',
  bz2: 'archive',
  xz: 'archive',
  zst: 'archive',
  jar: 'archive',
  war: 'archive',
  iso: 'archive',
  pdf: 'pdf',
  doc: 'doc',
  docx: 'doc',
  odt: 'doc',
  rtf: 'doc',
  ppt: 'doc',
  pptx: 'doc',
  xls: 'sheet',
  xlsx: 'sheet',
  ods: 'sheet',
  csv: 'sheet',
  txt: 'text',
  md: 'text',
  log: 'text',
  ini: 'text',
  cfg: 'text',
  conf: 'text',
  js: 'code',
  ts: 'code',
  tsx: 'code',
  jsx: 'code',
  go: 'code',
  py: 'code',
  java: 'code',
  c: 'code',
  h: 'code',
  cpp: 'code',
  cs: 'code',
  rb: 'code',
  php: 'code',
  rs: 'code',
  sh: 'code',
  json: 'code',
  yaml: 'code',
  yml: 'code',
  xml: 'code',
  html: 'code',
  htm: 'code',
  css: 'code',
  sql: 'code',
  toml: 'code',
}

/** Icon category of a member from its extension (the server does not sniff archive members on listing). */
export function entryCategory(e: Pick<ArchiveEntry, 'name' | 'isDir'>): string {
  if (e.isDir) return 'folder'
  const i = e.name.lastIndexOf('.')
  if (i <= 0) return 'other'
  return EXT_CATEGORY[e.name.slice(i + 1).toLowerCase()] ?? 'other'
}

const TEXT_PREVIEW_EXT = new Set([
  'txt',
  'md',
  'log',
  'ini',
  'cfg',
  'conf',
  'json',
  'yaml',
  'yml',
  'xml',
  'csv',
  'tsv',
  'html',
  'htm',
  'css',
  'js',
  'ts',
  'tsx',
  'jsx',
  'go',
  'py',
  'java',
  'c',
  'h',
  'cpp',
  'cs',
  'rb',
  'php',
  'rs',
  'sh',
  'sql',
  'toml',
  'env',
  'gitignore',
  'dockerfile',
  'makefile',
])
const IMAGE_PREVIEW_EXT = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'avif', 'ico'])

export const TEXT_PREVIEW_MAX = 1 << 20
export const IMAGE_PREVIEW_MAX = 20 << 20

/** What the in-archive preview can show (SPEC §8.11: text ≤ 1 MB, image ≤ 20 MB; the rest downloads). */
export function entryPreviewKind(e: ArchiveEntry): 'text' | 'image' | 'none' {
  if (e.isDir) return 'none'
  const i = e.name.lastIndexOf('.')
  const ext = i >= 0 ? e.name.slice(i + 1).toLowerCase() : e.name.toLowerCase()
  if (IMAGE_PREVIEW_EXT.has(ext) && e.size <= IMAGE_PREVIEW_MAX) return 'image'
  if ((TEXT_PREVIEW_EXT.has(ext) || i < 0) && e.size <= TEXT_PREVIEW_MAX) return 'text'
  return 'none'
}

/** Breadcrumb segments of an inner path: [{name, inner}]. */
export function innerCrumbs(inner: string): { name: string; inner: string }[] {
  const out: { name: string; inner: string }[] = []
  let acc = ''
  for (const seg of inner.split('/').filter(Boolean)) {
    acc = acc ? `${acc}/${seg}` : seg
    out.push({ name: seg, inner: acc })
  }
  return out
}

export function innerParent(inner: string): string {
  const i = inner.lastIndexOf('/')
  return i < 0 ? '' : inner.slice(0, i)
}
