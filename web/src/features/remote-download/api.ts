import type { ConflictPolicy } from '@/features/file-ops/api'
import type { Task } from '@/features/tasks/api'
import { api } from '@/lib/api'

/** `POST /remote-downloads` (SPEC §8.10): one task per URL, `202 {tasks}`. */
export interface RemoteDownloadRequest {
  urls: string[]
  path: string
  filename?: string | undefined
  onConflict: ConflictPolicy
}

export function createRemoteDownloads(req: RemoteDownloadRequest): Promise<{ tasks: Task[] }> {
  return api<{ tasks: Task[] }>('/remote-downloads', { method: 'POST', body: req })
}

export interface ProbeResult {
  finalUrl: string
  size: number | null
  filename?: string
  acceptsRanges: boolean
  contentType?: string
}

export function probeRemote(url: string): Promise<ProbeResult> {
  return api<ProbeResult>('/remote-downloads/probe', { method: 'POST', body: { url } })
}

export const MAX_REMOTE_URLS = 10

export type UrlProblem = 'empty' | 'invalid' | 'scheme' | 'userinfo' | 'tooMany'

/** Client-side shape check mirroring the server's URL policy (the server
 *  decides; this only saves a round trip). Returns the parsed list. */
export function parseUrlLines(text: string): { urls: string[]; problems: UrlProblem[] } {
  const lines = text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l.length > 0)
  const problems: UrlProblem[] = []
  const urls: string[] = []
  if (lines.length === 0) problems.push('empty')
  if (lines.length > MAX_REMOTE_URLS) problems.push('tooMany')
  for (const line of lines) {
    let u: URL
    try {
      u = new URL(line)
    } catch {
      problems.push('invalid')
      continue
    }
    if (u.protocol !== 'http:' && u.protocol !== 'https:') problems.push('scheme')
    else if (u.username || u.password) problems.push('userinfo')
    else urls.push(line)
  }
  return { urls, problems: [...new Set(problems)] }
}
