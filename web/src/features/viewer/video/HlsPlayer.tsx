import { Download, Loader2, X } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import type { Item } from '@/features/browser/api'
import { cancelTask, type Task } from '@/features/tasks/api'
import { ApiError } from '@/lib/api'
import { announce } from '@/lib/announce'
import { errorMessage } from '@/lib/errors'
import { formatDuration } from '@/lib/format'
import { isSignatureExpired } from '@/lib/media-url'
import { useTasksStore } from '@/stores/tasks'
import { playlistReadiness, startTranscode, type HlsSource } from './hls'

const POLL_MS = 2000

type LooseT = (key: string, options?: Record<string, unknown>) => string

function startError(t: LooseT, e: unknown): string {
  if (e instanceof ApiError && typeof e.details.reason === 'string') {
    const msg = t(`viewer:video.transcodeError.${e.details.reason}`, {
      defaultValue: '',
      max: e.details.maxMinutes,
    })
    if (msg) return msg
  }
  return errorMessage(e)
}

/** "Putar dengan konversi" (Step 23d): starts (or rejoins) the conversion,
 *  shows its progress with ETA and "Batalkan", polls the playlist every 2 s
 *  and hands the signed playlist to `children` (the video player body with
 *  `useHlsPlayback`) once two segments exist. Closing the viewer does not
 *  cancel the task (it stays in the task panel). */
export function HlsPlayer({
  item,
  onDownload,
  onBack,
  children,
}: {
  item: Item
  onDownload: () => void
  onBack: () => void
  children: (src: HlsSource) => ReactNode
}) {
  const { t } = useTranslation('viewer')
  const lt = t as unknown as LooseT
  const [taskId, setTaskId] = useState<string | null>(null)
  const [url, setUrl] = useState<string | null>(null)
  const [ready, setReady] = useState(false)
  const [finished, setFinished] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const task = useTasksStore((s) => (taskId ? s.tasks[taskId] : undefined))
  const path = item.path

  // start or rejoin
  useEffect(() => {
    let alive = true
    startTranscode(path).then(
      (res) => {
        if (!alive) return
        useTasksStore.getState().upsert(res.task)
        setTaskId(res.task.id)
        setUrl(res.playlistUrl)
        setFinished(res.task.state === 'done')
        if (res.ready) setReady(true)
        else announce(t('video.transcoding'))
      },
      (e: unknown) => {
        if (!alive) return
        const msg = startError(lt, e)
        setError(msg)
        announce(msg, true)
      },
    )
    return () => {
      alive = false
    }
  }, [path, t, lt])

  // poll the playlist until two segments exist (or ffmpeg ended the list)
  const stopped = error !== null || task?.state === 'failed' || task?.state === 'interrupted'
  useEffect(() => {
    if (!url || ready || stopped) return
    let alive = true
    let timer = 0
    const poll = async () => {
      try {
        const res = await fetch(url, { credentials: 'omit' })
        if (await isSignatureExpired(res)) {
          const fresh = await startTranscode(path)
          if (!alive) return
          setUrl(fresh.playlistUrl)
          return
        }
        if (res.status === 200) {
          const state = playlistReadiness(await res.text())
          if (!alive) return
          if (state.ready) {
            setFinished((f) => f || state.ended)
            setReady(true)
            return
          }
        }
      } catch {
        /* network hiccup: try again */
      }
      if (alive) timer = window.setTimeout(() => void poll(), POLL_MS)
    }
    void poll()
    return () => {
      alive = false
      window.clearTimeout(timer)
    }
  }, [url, ready, stopped, path])

  // a cancelled task returns to the card with the button
  const cancelled = !ready && task?.state === 'cancelled'
  useEffect(() => {
    if (cancelled) onBack()
  }, [cancelled, onBack])
  // the task ended without a playable result (the card's alert announces it)
  const taskError =
    !ready && task && (task.state === 'failed' || task.state === 'interrupted')
      ? task.error?.code === 'FORBIDDEN'
        ? t('video.transcodeError.transcode_disabled')
        : t('video.transcodeFailed')
      : null
  const shownError = error ?? taskError

  const resign = useCallback(async () => (await startTranscode(path)).playlistUrl, [path])
  // stable while playing: the player re-attaches only when the URL changes
  const source = useMemo<HlsSource | null>(
    () => (ready && url ? { url, finished, resign } : null),
    [ready, url, finished, resign],
  )
  if (source) return <>{children(source)}</>
  return (
    <TranscodeCard
      task={task}
      error={shownError}
      onDownload={onDownload}
      onBack={onBack}
      onCancel={
        task && (task.state === 'queued' || task.state === 'running')
          ? () => {
              void cancelTask(task.id)
                .then(({ task: x }) => useTasksStore.getState().upsert(x))
                .catch(() => undefined)
              onBack()
            }
          : undefined
      }
    />
  )
}

function TranscodeCard({
  task,
  error,
  onDownload,
  onBack,
  onCancel,
}: {
  task: Task | undefined
  error: string | null
  onDownload: () => void
  onBack: () => void
  onCancel: (() => void) | undefined
}) {
  const { t } = useTranslation('viewer')
  const percent = task?.progress.percent ?? 0
  const queued = task?.state === 'queued'
  const total = task?.progress.total ?? 0
  return (
    <div className="flex h-full items-center justify-center p-6" data-testid="video-transcode-card">
      <div className="flex w-full max-w-md flex-col items-center gap-3 rounded-lg border border-border bg-surface p-6 text-center">
        {error ? (
          <p className="text-sm text-danger" role="alert" data-testid="video-transcode-error">
            {error}
          </p>
        ) : (
          <>
            <p className="flex items-center gap-2 text-sm text-text" role="status">
              <Loader2 aria-hidden="true" className="size-4 animate-spin" />
              {queued
                ? t('video.transcodeQueued')
                : task
                  ? t('video.transcoding')
                  : t('video.transcodeStarting')}
            </p>
            <Progress
              value={percent}
              aria-label={t('video.transcodeProgressLabel')}
              aria-valuetext={`${percent} %`}
              data-testid="video-transcode-progress"
            />
            <p className="text-xs text-text-muted tabular" data-testid="video-transcode-meta">
              {task && total > 0
                ? t('video.transcodeMeta', {
                    percent: Math.floor(percent),
                    done: formatDuration(task.progress.done),
                    total: formatDuration(total),
                  })
                : null}
              {task?.etaSeconds != null && task.etaSeconds >= 0
                ? ` · ${t('video.transcodeEta', { eta: formatDuration(task.etaSeconds) })}`
                : null}
            </p>
            <p className="text-xs text-text-muted">{t('video.transcodeHint')}</p>
          </>
        )}
        <div className="flex flex-wrap justify-center gap-2">
          {onCancel && !error ? (
            <Button variant="secondary" onClick={onCancel} data-testid="video-transcode-cancel">
              <X aria-hidden="true" />
              {t('video.transcodeCancel')}
            </Button>
          ) : (
            <Button variant="secondary" onClick={onBack} data-testid="video-transcode-back">
              {t('video.transcodeBack')}
            </Button>
          )}
          <Button variant="ghost" onClick={onDownload}>
            <Download aria-hidden="true" />
            {t('card.download')}
          </Button>
        </div>
      </div>
    </div>
  )
}
