/** Search API client (SPEC §6.3 "Search", §8.12; Step 15). */
import type { Item } from '@/features/browser/api'
import type { Task } from '@/features/tasks/api'
import { api, API_BASE, ApiError, type Query } from '@/lib/api'
import { useSessionStore } from '@/stores/session'

export type SearchKind = 'name' | 'content'

export interface NameFilters {
  types?: string[]
  ext?: string
  minSize?: number
  maxSize?: number
  from?: string
  to?: string
  tag?: string
  recursive?: boolean
  glob?: boolean
  regex?: boolean
}

export interface NameItem extends Item {
  location: string
}

export interface NameSearchResponse {
  items: NameItem[]
  total: number
  nextCursor?: string
  truncated: boolean
  tookMs: number
  path: string
}

export function nameQueryKey(q: string, path: string, f: NameFilters) {
  return ['search', 'names', q, path, f] as const
}

export function searchNames(
  q: string,
  path: string,
  f: NameFilters,
  cursor?: string,
  signal?: AbortSignal,
): Promise<NameSearchResponse> {
  const query: Query = {
    q: q || undefined,
    path,
    recursive: f.recursive === false ? '0' : undefined,
    glob: f.glob ? '1' : undefined,
    regex: f.regex ? '1' : undefined,
    types: f.types?.length ? f.types.join(',') : undefined,
    ext: f.ext || undefined,
    minSize: f.minSize || undefined,
    maxSize: f.maxSize || undefined,
    from: f.from || undefined,
    to: f.to || undefined,
    tag: f.tag || undefined,
    cursor: cursor || undefined,
    limit: 500,
  }
  return api<NameSearchResponse>('/search/names', signal ? { query, signal } : { query })
}

export interface ContentQuery {
  q: string
  path: string
  regex?: boolean
  caseSensitive?: boolean
  wholeWord?: boolean
  recursive?: boolean
  ext?: string[]
  minSize?: number
  maxSize?: number
  from?: string
  to?: string
  maxMatches?: number
}

export interface ContentMatch {
  file: string
  line: number
  col: number
  len?: number
  text: string
  before?: string
  after?: string
}

export interface ContentSummary {
  done: true
  files: number
  matches: number
  truncated: boolean
  engine: 'rg' | 'go'
  tookMs?: number
  error?: string
}

async function csrf(): Promise<string> {
  const cur = useSessionStore.getState().csrfToken
  if (cur) return cur
  const data = await api<{ csrfToken: string }>('/auth/csrf')
  useSessionStore.getState().setCsrfToken(data.csrfToken)
  return data.csrfToken
}

/** Streams `POST /search/content` (NDJSON); `onMatch` is called per row and the
 *  summary line resolves the promise. Errors before the stream starts throw ApiError. */
export async function searchContent(
  q: ContentQuery,
  onMatch: (m: ContentMatch) => void,
  signal?: AbortSignal,
): Promise<ContentSummary> {
  const res = await fetch(`${API_BASE}/search/content`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/x-ndjson',
      'X-CSRF-Token': await csrf(),
    },
    body: JSON.stringify(q),
    signal: signal ?? null,
  })
  if (!res.ok) {
    let body: { error?: { code?: string; message?: string; details?: Record<string, unknown> } } =
      {}
    try {
      body = (await res.json()) as typeof body
    } catch {
      /* non-JSON */
    }
    const details: Record<string, unknown> = { ...(body.error?.details ?? {}) }
    const ra = res.headers.get('Retry-After')
    if (ra && /^\d+$/.test(ra)) details.retryAfter = Number(ra)
    throw new ApiError(
      body.error?.code ?? 'UNKNOWN',
      res.status,
      body.error?.message ?? res.statusText,
      details,
    )
  }
  if (!res.body) throw new ApiError('INTERNAL', 500, 'empty stream')
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  let summary: ContentSummary | null = null
  const handle = (line: string) => {
    if (!line.trim()) return
    const row = JSON.parse(line) as ContentMatch | ContentSummary
    if ('done' in row && row.done) summary = row
    else onMatch(row as ContentMatch)
  }
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    let nl = buf.indexOf('\n')
    while (nl >= 0) {
      handle(buf.slice(0, nl))
      buf = buf.slice(nl + 1)
      nl = buf.indexOf('\n')
    }
  }
  if (buf.trim()) handle(buf)
  if (!summary) throw new ApiError('INTERNAL', 500, 'stream ended without summary')
  return summary
}

export interface ReplaceHunk {
  line: number
  before: string
  after: string
}

export interface ReplacePreviewFile {
  path: string
  replacements: number
  hunks?: ReplaceHunk[]
  hunksTotal: number
  skipped?: 'encoding' | 'too_large' | 'binary' | 'unreadable'
}

export interface ReplacePreviewResult {
  previewId: string
  files: ReplacePreviewFile[]
  totalFiles: number
  totalReplacements: number
  truncated: boolean
  engine: string
  expiresAt: string
}

export interface ReplaceApplyFile {
  path: string
  status: 'ok' | 'skipped'
  reason?: string
  replacements: number
  versionId?: string
}

export interface ReplaceApplyResult {
  files: ReplaceApplyFile[]
  replacements: number
  versionIds: string[]
  skipped: number
}

export function previewReplace(q: ContentQuery, replace: string): Promise<{ task: Task }> {
  return api<{ task: Task }>('/search/replace/preview', { method: 'POST', body: { ...q, replace } })
}

export function applyReplace(previewId: string, files?: string[]): Promise<{ task: Task }> {
  return api<{ task: Task }>('/search/replace', {
    method: 'POST',
    body: files?.length ? { previewId, files } : { previewId },
  })
}

export interface HistoryEntry {
  id: number
  kind: SearchKind
  query: string
  path: string
  filters?: Record<string, unknown>
  at: string
}

export const HISTORY_QUERY_KEY = ['search', 'history'] as const

export function getSearchHistory(signal?: AbortSignal): Promise<{ items: HistoryEntry[] }> {
  return api<{ items: HistoryEntry[] }>('/search/history', signal ? { signal } : {})
}

export function clearSearchHistory(): Promise<void> {
  return api('/search/history', { method: 'DELETE' })
}

export interface FileVersion {
  id: string
  path: string
  no: number
  size: number
  sha256: string
  reason: string
  createdAt: string
}

export function restoreVersion(id: string): Promise<{ path: string; size: number }> {
  return api<{ path: string; size: number }>(`/versions/${encodeURIComponent(id)}/restore`, {
    method: 'POST',
  })
}

export function listVersions(
  path: string,
): Promise<{ items: FileVersion[]; limit: number; enabled: boolean }> {
  return api('/versions', { query: { path } })
}

/** Polls a task until it is final (the task panel also follows it over SSE). */
export async function waitForTask(id: string, signal?: AbortSignal): Promise<Task> {
  const { getTask } = await import('@/features/tasks/api')
  for (;;) {
    const { task } = await getTask(id)
    if (task.state !== 'queued' && task.state !== 'running') return task
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(resolve, 400)
      signal?.addEventListener('abort', () => {
        clearTimeout(timer)
        reject(new DOMException('aborted', 'AbortError'))
      })
    })
  }
}
