/** Listing API client (SPEC §6.3, §8.3). Types mirror `listing.Item`/`ListResult`. */
import { api, type Query } from '@/lib/api'

export interface MediaMeta {
  width?: number
  height?: number
  duration?: number
}

export interface Item {
  name: string
  path: string
  type: 'dir' | 'file' | 'symlink'
  isDir: boolean
  isSymlink: boolean
  symlinkTarget?: string
  broken?: boolean
  brokenReason?: string
  size: number
  itemCount: number | null
  mime: string | null
  typeLabel: string
  category: string
  ext: string
  mtime: string
  ctime: string
  btime: string | null
  atime: string
  mode: string
  modeOctal: string
  note: string | null
  tag: string | null
  favorite: boolean
  pinned: boolean
  shared: 'none' | 'public' | 'users' | 'both'
  media: MediaMeta | null
  thumb: boolean
  etag: string
}

export interface DirInfo {
  path: string
  etag: string
  mtime: string
  itemCount: number
}

export interface ListResponse {
  items: Item[]
  nextCursor: string | null
  total: number
  path: string
  parentPath: string | null
  dir: DirInfo
}

export type SortField =
  | 'name'
  | 'size'
  | 'type'
  | 'ext'
  | 'mtime'
  | 'btime'
  | 'atime'
  | 'mode'
  | 'note'
  | 'dimensions'
  | 'duration'
export type SortDir = 'asc' | 'desc'

export interface ListOptions {
  sort?: SortField
  dir?: SortDir
  sort2?: SortField | undefined
  dir2?: SortDir | undefined
  foldersFirst?: boolean
  hidden?: boolean
  types?: string
  filter?: string
  limit?: number
  counts?: boolean
}

export const DEFAULT_LIST: Required<Pick<ListOptions, 'sort' | 'dir' | 'foldersFirst'>> = {
  sort: 'name',
  dir: 'asc',
  foldersFirst: true,
}

export function listQueryKey(path: string, opts: ListOptions = {}) {
  const o = { ...DEFAULT_LIST, ...opts }
  return [
    'list',
    path,
    o.sort,
    o.dir,
    o.sort2 ?? '',
    o.dir2 ?? '',
    o.foldersFirst,
    o.types ?? '',
    o.filter ?? '',
  ] as const
}

export function listDir(
  path: string,
  opts: ListOptions = {},
  cursor: string | null = null,
  signal?: AbortSignal,
): Promise<ListResponse> {
  const o = { ...DEFAULT_LIST, ...opts }
  const query: Query = {
    path,
    sort: o.sort,
    dir: o.dir,
    sort2: o.sort2,
    dir2: o.sort2 ? o.dir2 : undefined,
    foldersFirst: o.foldersFirst,
    hidden: o.hidden,
    types: o.types,
    filter: o.filter,
    limit: o.limit,
    counts: o.counts,
    cursor,
  }
  return api<ListResponse>('/files/list', signal ? { query, signal } : { query })
}

export interface MutationResponse {
  item: Item
  warnings: string[]
}

export function mkdir(path: string, name: string): Promise<MutationResponse> {
  return api<MutationResponse>('/files/mkdir', { method: 'POST', body: { path, name } })
}

export function createFile(
  path: string,
  name: string,
  template?: string,
): Promise<MutationResponse> {
  const body: Record<string, unknown> = { path, name }
  if (template) body.template = template
  return api<MutationResponse>('/files/create', { method: 'POST', body })
}

export interface StatDetails {
  mode: string
  modeOctal: string
  atime: string
  mtime: string
  ctime: string
  btime: string | null
  nlink: number
}

export interface StatResponse extends Item {
  stat: StatDetails
  warnings: string[]
  versionsCount: number
  shares: unknown[]
}

export function statItem(path: string, signal?: AbortSignal): Promise<StatResponse> {
  return api<StatResponse>(
    '/files/stat',
    signal ? { query: { path }, signal } : { query: { path } },
  )
}

/** Folder view preference (SPEC §8.18 `folder_view_prefs`). */
export interface FolderPref {
  path: string
  view: 'details' | 'list' | 'tiles' | 'large' | 'xlarge'
  sort: { field: SortField; dir: SortDir; field2?: SortField; dir2?: SortDir }
  columns: { visible?: string[]; order?: string[]; widths?: Record<string, number> } | null
  updatedAt: string
}

export function getFolderPref(path: string, signal?: AbortSignal): Promise<FolderPref | null> {
  return api<{ pref: FolderPref | null }>(
    '/me/folder-prefs',
    signal ? { query: { path }, signal } : { query: { path } },
  ).then((r) => r.pref)
}

export interface FolderPrefInput {
  path: string
  view?: FolderPref['view']
  sort?: SortField
  dir?: SortDir
  sort2?: SortField
  dir2?: SortDir
  columns?: { visible: string[]; order: string[]; widths: Record<string, number> }
}

export function putFolderPref(input: FolderPrefInput): Promise<FolderPref> {
  return api<{ pref: FolderPref }>('/me/folder-prefs', { method: 'PUT', body: input }).then(
    (r) => r.pref,
  )
}

export function deleteFolderPref(path: string): Promise<void> {
  return api<void>('/me/folder-prefs', { method: 'DELETE', query: { path } })
}
