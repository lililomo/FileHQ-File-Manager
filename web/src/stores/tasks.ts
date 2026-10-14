import { create } from 'zustand'
import { isActive, type Task } from '@/features/tasks/api'
import type { Notification } from '@/features/notifications/api'

/** Tasks shown in the panel: live ones plus the last RECENT_LIMIT finished. */
export const RECENT_LIMIT = 20

export type ConnectionStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface TasksState {
  tasks: Record<string, Task>
  /** Newest first. */
  order: string[]
  panelOpen: boolean
  connection: ConnectionStatus
  notifications: Notification[]
  unread: number
  bellOpen: boolean
  upsert: (task: Task) => void
  setAll: (tasks: Task[]) => void
  remove: (id: string) => void
  setPanel: (open: boolean) => void
  setConnection: (s: ConnectionStatus) => void
  setNotifications: (items: Notification[], unread: number) => void
  pushNotification: (n: Notification) => void
  markRead: (ids: string[] | 'all', unread?: number) => void
  removeNotification: (id: string) => void
  setBell: (open: boolean) => void
}

function sortOrder(tasks: Record<string, Task>): string[] {
  return Object.values(tasks)
    .sort((a, b) => (a.createdAt < b.createdAt ? 1 : a.createdAt > b.createdAt ? -1 : 0))
    .map((t) => t.id)
}

/** Keep every active task and only the newest RECENT_LIMIT finished ones. */
function prune(tasks: Record<string, Task>): Record<string, Task> {
  const finished = Object.values(tasks)
    .filter((t) => !isActive(t))
    .sort((a, b) => (a.createdAt < b.createdAt ? 1 : -1))
  if (finished.length <= RECENT_LIMIT) return tasks
  const out = { ...tasks }
  for (const t of finished.slice(RECENT_LIMIT)) delete out[t.id]
  return out
}

export const useTasksStore = create<TasksState>((set) => ({
  tasks: {},
  order: [],
  panelOpen: false,
  connection: 'closed',
  notifications: [],
  unread: 0,
  bellOpen: false,
  upsert: (task) =>
    set((s) => {
      const prev = s.tasks[task.id]
      // never let a stale progress event overwrite a final state
      if (prev && !isActive(prev) && isActive(task)) return {}
      const tasks = prune({ ...s.tasks, [task.id]: task })
      return { tasks, order: sortOrder(tasks) }
    }),
  setAll: (list) =>
    set(() => {
      const tasks: Record<string, Task> = {}
      for (const t of list) tasks[t.id] = t
      const pruned = prune(tasks)
      return { tasks: pruned, order: sortOrder(pruned) }
    }),
  remove: (id) =>
    set((s) => {
      if (!s.tasks[id]) return {}
      const tasks = { ...s.tasks }
      delete tasks[id]
      return { tasks, order: sortOrder(tasks) }
    }),
  setPanel: (panelOpen) => set({ panelOpen }),
  setConnection: (connection) => set({ connection }),
  setNotifications: (notifications, unread) => set({ notifications, unread }),
  pushNotification: (n) =>
    set((s) => {
      if (s.notifications.some((x) => x.id === n.id)) return {}
      return {
        notifications: [n, ...s.notifications].slice(0, 50),
        unread: s.unread + (n.readAt ? 0 : 1),
      }
    }),
  markRead: (ids, unread) =>
    set((s) => {
      const now = new Date().toISOString()
      const set2 = ids === 'all' ? null : new Set(ids)
      let changed = 0
      const notifications = s.notifications.map((n) => {
        if (n.readAt || (set2 && !set2.has(n.id))) return n
        changed += 1
        return { ...n, readAt: now }
      })
      return {
        notifications,
        unread: unread ?? (ids === 'all' ? 0 : Math.max(0, s.unread - changed)),
      }
    }),
  removeNotification: (id) =>
    set((s) => {
      const n = s.notifications.find((x) => x.id === id)
      if (!n) return {}
      return {
        notifications: s.notifications.filter((x) => x.id !== id),
        unread: n.readAt ? s.unread : Math.max(0, s.unread - 1),
      }
    }),
  setBell: (bellOpen) => set({ bellOpen }),
}))

/** Number of queued/running tasks (top bar badge). */
export function activeCount(tasks: Record<string, Task>): number {
  let n = 0
  for (const t of Object.values(tasks)) if (isActive(t)) n += 1
  return n
}

export function resetTasksStore(): void {
  useTasksStore.setState({
    tasks: {},
    order: [],
    panelOpen: false,
    connection: 'closed',
    notifications: [],
    unread: 0,
    bellOpen: false,
  })
}
