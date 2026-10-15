/** Editor API client (SPEC §8.13, §6.3 `files/content`, `me/editor-session`; Step 16a). */
import { api } from '@/lib/api'

export type EOL = 'lf' | 'crlf' | 'mixed'

export interface FileContent {
  path: string
  content: string
  encoding: string
  detectedEncoding: string
  bom: boolean
  lossy: boolean
  eol: EOL
  language: string
  size: number
  mtime: string
  etag: string
  mode: string
  lines: number
  truncated: boolean
  offset?: number
  tailLines?: number
  readOnly: boolean
}

export interface ContentQuery {
  encoding?: string
  tail?: number
}

export function getContent(
  path: string,
  q: ContentQuery = {},
  signal?: AbortSignal,
): Promise<FileContent> {
  return api<FileContent>('/files/content', {
    query: { path, encoding: q.encoding, tail: q.tail },
    ...(signal ? { signal } : {}),
  })
}

export interface SaveInput {
  path: string
  content: string
  encoding: string
  bom: boolean
  eol: 'keep' | 'lf' | 'crlf'
  etag?: string | undefined
  force?: boolean
  create?: boolean
}

export interface SaveResult {
  etag: string
  mtime: string
  size: number
  version: { id: string; no: number } | null
  noop: boolean
  created?: boolean
}

export function saveContent(input: SaveInput): Promise<SaveResult> {
  const { etag, ...body } = input
  return api<SaveResult>('/files/content', {
    method: 'PUT',
    body,
    headers: etag && !input.force ? { 'If-Match': etag } : {},
  })
}

export interface SessionTab {
  path: string
  scrollTop?: number
  cursor?: { line: number; col: number }
  language?: string
  encoding?: string
}

export interface EditorSession {
  tabs: SessionTab[]
  activePath: string | null
  updatedAt?: string
}

export function getEditorSession(signal?: AbortSignal): Promise<EditorSession> {
  return api<EditorSession>('/me/editor-session', signal ? { signal } : {})
}

export function putEditorSession(session: EditorSession): Promise<EditorSession> {
  return api<EditorSession>('/me/editor-session', { method: 'PUT', body: session })
}

/** Encodings the server accepts (SPEC §8.13 step 3). */
export const ENCODINGS = [
  'utf-8',
  'utf-16le',
  'utf-16be',
  'windows-1252',
  'iso-8859-1',
  'iso-8859-2',
  'iso-8859-15',
  'windows-1250',
  'windows-1251',
  'koi8-r',
  'gbk',
  'gb18030',
  'big5',
  'shift_jis',
  'euc-jp',
  'iso-2022-jp',
  'euc-kr',
] as const
