import { formatBytes, formatDuration, formatNumber } from '@/lib/format'
import type { Task } from './api'

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** Localised title from type + payload; falls back to the server title. */
export function taskLabel(task: Task, t: LooseT, lang: string): string {
  const p = task.payload ?? {}
  const paths = Array.isArray(p.paths) ? (p.paths as string[]) : []
  const count = paths.length
  const to = typeof p.to === 'string' ? p.to : ''
  const name = (s: unknown) => (typeof s === 'string' ? s.split('/').pop() || s : '')
  switch (task.type) {
    case 'copy':
    case 'move':
      if (count === 1 && to)
        return t(`tasks:label.${task.type}One`, { name: name(paths[0]), to, count })
      if (to) return t(`tasks:label.${task.type}`, { count, n: formatNumber(count, lang), to })
      break
    case 'dir_size':
      if (p.analysis === true) return t('tasks:label.analysis')
      return t('tasks:label.dirSize', { name: name(p.path) || '/' })
    case 'checksum':
      return t('tasks:label.checksum', { name: name(p.path) })
    case 'remote_download': {
      const dest = typeof p.dest === 'string' ? p.dest : ''
      let label = typeof p.filename === 'string' ? p.filename : ''
      if (!label && typeof p.url === 'string') {
        try {
          const u = new URL(p.url)
          const seg = decodeURIComponent(u.pathname.replace(/\/+$/, '').split('/').pop() ?? '')
          label = seg || u.host
        } catch {
          label = p.url
        }
      }
      if (dest) return t('tasks:label.remoteDownload', { name: label, to: dest })
      break
    }
  }
  return task.title
}

/** "1,2 MB/s · 00:12 tersisa" style secondary line. */
export function taskMeta(task: Task, t: LooseT, lang: string): string {
  const parts: string[] = []
  const { progress } = task
  if (task.state === 'running') {
    if (progress.total > 0) {
      parts.push(
        progress.unit === 'bytes'
          ? `${formatBytes(progress.done, lang)} / ${formatBytes(progress.total, lang)}`
          : t('tasks:meta.items', {
              done: formatNumber(progress.done, lang),
              total: formatNumber(progress.total, lang),
            }),
      )
    } else if (progress.done > 0) {
      parts.push(
        progress.unit === 'bytes'
          ? formatBytes(progress.done, lang)
          : t('tasks:meta.itemsDone', { done: formatNumber(progress.done, lang) }),
      )
    }
    if (task.speedBps && task.speedBps > 0) {
      parts.push(
        progress.unit === 'bytes'
          ? t('tasks:meta.speedBytes', { speed: formatBytes(task.speedBps, lang) })
          : t('tasks:meta.speedItems', { speed: formatNumber(task.speedBps, lang) }),
      )
    }
    if (task.etaSeconds !== null && task.etaSeconds >= 0) {
      parts.push(t('tasks:meta.eta', { eta: formatDuration(task.etaSeconds) }))
    }
  } else if (task.state === 'failed' || task.state === 'interrupted') {
    if (task.error?.message) parts.push(task.error.message)
    else if (task.error?.code) parts.push(task.error.code)
  } else if (task.state === 'done' && progress.total > 0 && progress.unit === 'bytes') {
    parts.push(formatBytes(progress.total, lang))
  }
  return parts.join(' · ')
}
