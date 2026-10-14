/** Task engine API (SPEC §6.3 "Tasks & events", §7). */
import { api } from '@/lib/api'

export type TaskState = 'queued' | 'running' | 'done' | 'failed' | 'cancelled' | 'interrupted'

export interface TaskProgress {
  done: number
  total: number
  unit: 'bytes' | 'items'
  percent: number
}

export interface Task {
  id: string
  type: string
  state: TaskState
  title: string
  payload: Record<string, unknown>
  result?: Record<string, unknown> | null
  error?: { code: string; message?: string }
  progress: TaskProgress
  etaSeconds: number | null
  speedBps: number | null
  cancelRequested: boolean
  cancelable: boolean
  retryable: boolean
  retryOf?: string
  createdAt: string
  startedAt: string | null
  finishedAt: string | null
}

export interface TaskLogEntry {
  id: number
  at: string
  level: 'info' | 'warn' | 'error'
  message: string
}

export const ACTIVE_STATES: TaskState[] = ['queued', 'running']
export const FINAL_STATES: TaskState[] = ['done', 'failed', 'cancelled', 'interrupted']

export function isActive(t: Pick<Task, 'state'>): boolean {
  return t.state === 'queued' || t.state === 'running'
}

export function listTasks(
  opts: { state?: TaskState; cursor?: string; limit?: number } = {},
  signal?: AbortSignal,
): Promise<{ items: Task[]; nextCursor?: string }> {
  const query: Record<string, string | number | undefined> = {
    state: opts.state,
    cursor: opts.cursor,
    limit: opts.limit,
  }
  return api('/tasks', signal ? { query, signal } : { query })
}

export function getTask(id: string): Promise<{ task: Task }> {
  return api(`/tasks/${encodeURIComponent(id)}`)
}

export function getTaskLogs(id: string, after = 0): Promise<{ items: TaskLogEntry[] }> {
  return api(`/tasks/${encodeURIComponent(id)}/logs`, { query: { after } })
}

export function cancelTask(id: string): Promise<{ task: Task }> {
  return api(`/tasks/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
}

export function retryTask(id: string): Promise<{ task: Task }> {
  return api(`/tasks/${encodeURIComponent(id)}/retry`, { method: 'POST' })
}

export function deleteTask(id: string): Promise<void> {
  return api(`/tasks/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/** `{task}` of a 202 answer, or null when the operation ran synchronously. */
export function taskOf(res: unknown): Task | null {
  if (!res || typeof res !== 'object') return null
  const t = (res as { task?: unknown }).task
  if (!t || typeof t !== 'object' || typeof (t as Task).id !== 'string') return null
  return t as Task
}
