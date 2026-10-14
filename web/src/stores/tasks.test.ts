import { beforeEach, describe, expect, it } from 'vitest'
import type { Task } from '@/features/tasks/api'
import { activeCount, RECENT_LIMIT, resetTasksStore, useTasksStore } from './tasks'

function task(id: string, state: Task['state'], createdAt: string): Task {
  return {
    id,
    type: 'copy',
    state,
    title: id,
    payload: {},
    progress: { done: 0, total: 0, unit: 'bytes', percent: 0 },
    etaSeconds: null,
    speedBps: null,
    cancelRequested: false,
    cancelable: state === 'queued' || state === 'running',
    retryable: false,
    createdAt,
    startedAt: null,
    finishedAt: null,
  }
}

describe('tasks store', () => {
  beforeEach(() => resetTasksStore())

  it('orders newest first, counts active tasks and ignores stale progress after a final state', () => {
    const st = useTasksStore.getState()
    st.upsert(task('a', 'running', '2026-10-14T10:00:00Z'))
    st.upsert(task('b', 'queued', '2026-10-14T10:00:01Z'))
    st.upsert(task('c', 'done', '2026-10-14T09:00:00Z'))
    expect(useTasksStore.getState().order).toEqual(['b', 'a', 'c'])
    expect(activeCount(useTasksStore.getState().tasks)).toBe(2)
    useTasksStore.getState().upsert(task('a', 'done', '2026-10-14T10:00:00Z'))
    useTasksStore.getState().upsert(task('a', 'running', '2026-10-14T10:00:00Z')) // stale
    expect(useTasksStore.getState().tasks.a!.state).toBe('done')
    useTasksStore.getState().remove('c')
    expect(useTasksStore.getState().order).toEqual(['b', 'a'])
  })

  it('keeps every active task but only the newest finished ones', () => {
    const st = useTasksStore.getState()
    for (let i = 0; i < RECENT_LIMIT + 5; i++) {
      st.upsert(task(`f${i}`, 'done', `2026-10-14T10:00:${String(i).padStart(2, '0')}Z`))
    }
    st.upsert(task('old-active', 'running', '2026-01-01T00:00:00Z'))
    const s = useTasksStore.getState()
    expect(Object.keys(s.tasks)).toHaveLength(RECENT_LIMIT + 1)
    expect(s.tasks['old-active']).toBeDefined()
    expect(s.tasks.f0).toBeUndefined()
    expect(s.tasks[`f${RECENT_LIMIT + 4}`]).toBeDefined()
  })

  it('tracks notifications and the unread badge', () => {
    const st = useTasksStore.getState()
    st.setNotifications(
      [
        {
          id: 'n1',
          type: 'task_done',
          title: 'x',
          readAt: null,
          createdAt: '2026-10-14T10:00:00Z',
        },
        {
          id: 'n2',
          type: 'task_done',
          title: 'y',
          readAt: '2026-10-14T10:01:00Z',
          createdAt: '2026-10-14T10:00:00Z',
        },
      ],
      1,
    )
    st.pushNotification({
      id: 'n3',
      type: 'task_failed',
      title: 'z',
      readAt: null,
      createdAt: '2026-10-14T10:02:00Z',
    })
    st.pushNotification({
      id: 'n3',
      type: 'task_failed',
      title: 'z',
      readAt: null,
      createdAt: '2026-10-14T10:02:00Z',
    })
    expect(useTasksStore.getState().unread).toBe(2)
    expect(useTasksStore.getState().notifications.map((n) => n.id)).toEqual(['n3', 'n1', 'n2'])
    useTasksStore.getState().markRead(['n3'])
    expect(useTasksStore.getState().unread).toBe(1)
    expect(useTasksStore.getState().notifications[0]!.readAt).not.toBeNull()
    useTasksStore.getState().removeNotification('n1')
    expect(useTasksStore.getState().unread).toBe(0)
    useTasksStore.getState().pushNotification({
      id: 'n4',
      type: 'system',
      title: 'w',
      readAt: null,
      createdAt: '2026-10-14T10:03:00Z',
    })
    useTasksStore.getState().markRead('all')
    expect(useTasksStore.getState().unread).toBe(0)
    expect(useTasksStore.getState().notifications.every((n) => n.readAt)).toBe(true)
  })
})
