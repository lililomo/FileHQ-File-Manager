import { describe, expect, it } from 'vitest'
import type { Task } from './api'
import { taskLabel, taskMeta } from './taskLabel'

// Step 23d: transcode_hls label and media-time progress (items = seconds).
const t = (key: string, o?: Record<string, unknown>) => `${key}${o ? JSON.stringify(o) : ''}`

function task(over: Partial<Task>): Task {
  return {
    id: '1',
    type: 'transcode_hls',
    state: 'running',
    title: 'Konversi film.mkv',
    payload: { path: '/video/film.mkv' },
    progress: { done: 65, total: 600, unit: 'items', percent: 10.8 },
    etaSeconds: 270,
    speedBps: 2,
    cancelRequested: false,
    cancelable: true,
    retryable: false,
    createdAt: '',
    startedAt: null,
    finishedAt: null,
    ...over,
  }
}

describe('transcode_hls tasks', () => {
  it('is labelled with the file name', () => {
    expect(taskLabel(task({}), t, 'id')).toBe('tasks:label.transcode{"name":"film.mkv"}')
    expect(taskLabel(task({ payload: {} }), t, 'id')).toBe('Konversi film.mkv')
  })
  it('shows converted media time, real-time factor and ETA', () => {
    expect(taskMeta(task({}), t, 'id')).toBe(
      'tasks:meta.mediaTime{"done":"1:05","total":"10:00"} · tasks:meta.speedRealtime{"speed":"2"} · tasks:meta.eta{"eta":"4:30"}',
    )
    expect(taskMeta(task({ speedBps: 0, etaSeconds: null }), t, 'id')).toBe(
      'tasks:meta.mediaTime{"done":"1:05","total":"10:00"}',
    )
  })
})
