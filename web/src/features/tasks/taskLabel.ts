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
    case 'duplicate_finder':
      return t('tasks:label.duplicates', { name: name(p.path) || '/' })
    case 'transcode_hls':
      if (typeof p.path === 'string') return t('tasks:label.transcode', { name: name(p.path) })
      break
    case 'compress': {
      const n = typeof p.name === 'string' ? p.name : ''
      if (n) return t('tasks:label.compress', { name: n })
      break
    }
    case 'extract': {
      const a = typeof p.archive === 'string' ? name(p.archive) : ''
      if (a) return t('tasks:label.extract', { name: a })
      break
    }
    case 'zip_edit': {
      const a = typeof p.archive === 'string' ? name(p.archive) : ''
      const sources = Array.isArray(p.sources) ? p.sources.length : 0
      const entries = Array.isArray(p.entries) ? p.entries.length : 0
      if (a && p.mode === 'add')
        return t('tasks:label.zipEditAdd', {
          name: a,
          n: formatNumber(sources, lang),
          count: sources,
        })
      if (a && p.mode === 'remove')
        return t('tasks:label.zipEditRemove', {
          name: a,
          n: formatNumber(entries, lang),
          count: entries,
        })
      break
    }
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
  if (MAINTENANCE_TYPES.includes(task.type)) return t(`tasks:label.job.${task.type}`)
  return task.title
}

/** System task types of the admin maintenance page (SPEC §7.1, Step 19b). */
export const MAINTENANCE_TYPES = [
  'recalc_quota',
  'clear_thumbs',
  'clean_uploads',
  'purge_trash',
  'integrity_check',
  'backup_db',
  'prune_logs',
]

/** "1,2 MB/s · 00:12 tersisa" style secondary line. */
export function taskMeta(task: Task, t: LooseT, lang: string): string {
  const parts: string[] = []
  const { progress } = task
  // transcode_hls counts seconds of media in "items" (Step 23d)
  if (task.type === 'transcode_hls' && task.state === 'running') {
    if (progress.total > 0)
      parts.push(
        t('tasks:meta.mediaTime', {
          done: formatDuration(progress.done),
          total: formatDuration(progress.total),
        }),
      )
    if (task.speedBps && task.speedBps > 0)
      parts.push(t('tasks:meta.speedRealtime', { speed: formatNumber(task.speedBps, lang) }))
    if (task.etaSeconds !== null && task.etaSeconds >= 0)
      parts.push(t('tasks:meta.eta', { eta: formatDuration(task.etaSeconds) }))
    return parts.join(' · ')
  }
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
