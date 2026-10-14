import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { QUOTA_QUERY_KEY } from '@/features/quota/api'
import { STORAGE_QUERY_KEY } from '@/features/storage-analysis/api'
import { announce } from '@/lib/announce'
import { API_BASE } from '@/lib/api'
import { SSEClient } from '@/lib/sse'
import { useSessionStore } from '@/stores/session'
import { useTasksStore } from '@/stores/tasks'
import { listNotifications, type Notification } from '@/features/notifications/api'
import { isActive, listTasks, type Task } from './api'
import { taskLabel } from './taskLabel'

/** Events the client subscribes to (SPEC §6.4). */
export const SSE_EVENTS = [
  'task.created',
  'task.progress',
  'task.done',
  'task.failed',
  'task.cancelled',
  'notification',
  'file.changed',
  'quota.updated',
  'session.revoked',
  'resync',
]

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** Opens the SSE stream while authenticated, feeds the tasks/notifications
 *  stores, invalidates listings on `file.changed`, refreshes the quota and
 *  shows the finish toasts (SPEC §7.4, §8.16). Mount once in the shell. */
export function useTaskEvents(): void {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const { i18n } = useTranslation(['tasks'])
  const authenticated = useSessionStore((s) => s.status === 'authenticated')

  useEffect(() => {
    if (!authenticated) return
    // translate at event time (the stream outlives language switches)
    const fixed = i18n.getFixedT(null, null) as unknown as LooseT
    const lt: LooseT = (key, options) => (options ? fixed(key, options) : fixed(key))
    const langOf = () => i18n.resolvedLanguage ?? 'id'
    const store = useTasksStore.getState()
    let cancelled = false
    const refetch = async () => {
      try {
        const [tasks, notes] = await Promise.all([
          listTasks({ limit: 50 }),
          listNotifications({ limit: 20 }),
        ])
        if (cancelled) return
        useTasksStore.getState().setAll(tasks.items)
        useTasksStore.getState().setNotifications(notes.items, notes.unread)
      } catch {
        /* the stream will deliver a resync later */
      }
    }
    void refetch()
    const finished = (task: Task, name: string) => {
      const label = taskLabel(task, lt, langOf())
      if (name === 'task.done') {
        const msg = lt('tasks:toast.done', { title: label })
        toast.success(msg)
        announce(msg)
      } else if (name === 'task.failed') {
        const reason = task.error?.message || task.error?.code || ''
        const msg = lt(
          task.state === 'interrupted' ? 'tasks:toast.interrupted' : 'tasks:toast.failed',
          { title: label, reason },
        )
        toast.error(msg)
        announce(msg, true)
      } else {
        const msg = lt('tasks:toast.cancelled', { title: label })
        toast.info(msg)
        announce(msg)
      }
      void qc.invalidateQueries({ queryKey: QUOTA_QUERY_KEY })
      if (task.type === 'dir_size' && task.payload?.analysis === true) {
        void qc.invalidateQueries({ queryKey: STORAGE_QUERY_KEY })
      }
    }
    const client = new SSEClient({
      url: `${API_BASE}/events`,
      events: SSE_EVENTS,
      shouldReconnect: () => useSessionStore.getState().status === 'authenticated',
      onStatus: (s) => useTasksStore.getState().setConnection(s),
      onEvent: (name, data) => {
        const st = useTasksStore.getState()
        switch (name) {
          case 'task.created':
          case 'task.progress':
            st.upsert(data as Task)
            break
          case 'task.done':
          case 'task.failed':
          case 'task.cancelled': {
            const task = data as Task
            const prev = st.tasks[task.id]
            st.upsert(task)
            // a task that this tab never saw active still deserves a toast
            if (!prev || isActive(prev)) finished(task, name)
            break
          }
          case 'notification':
            st.pushNotification(data as Notification)
            break
          case 'file.changed': {
            const ev = data as { dir?: string }
            if (ev?.dir) void qc.invalidateQueries({ queryKey: ['list', ev.dir] })
            break
          }
          case 'quota.updated': {
            const q = data as { used: number; trash: number; versions: number; limit: number }
            if (q && typeof q.used === 'number') {
              useSessionStore.getState().updateUser({
                quota: { limit: q.limit, used: q.used, trash: q.trash, versions: q.versions },
              })
              void qc.invalidateQueries({ queryKey: QUOTA_QUERY_KEY })
            }
            break
          }
          case 'session.revoked':
            useSessionStore.getState().setAnonymous()
            void navigate({ to: '/login', search: { next: undefined } })
            break
          case 'resync':
            void refetch()
            break
        }
      },
    })
    client.connect()
    return () => {
      cancelled = true
      client.close()
      store.setConnection('closed')
    }
  }, [authenticated, qc, navigate, i18n])
}
