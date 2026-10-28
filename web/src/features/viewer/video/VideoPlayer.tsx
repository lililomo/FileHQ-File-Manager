import { Container, Controls, TimeSlider } from '@videojs/react'
import { usePlayer, Video, VideoPlayer as Player } from '@videojs/react/video'
import { useQuery } from '@tanstack/react-query'
import {
  Captions,
  Download,
  Film,
  Maximize,
  Minimize,
  Pause,
  PictureInPicture2,
  Play,
  RotateCcw,
  SkipForward,
  Volume2,
  VolumeX,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import type { Item } from '@/features/browser/api'
import { getMediaInfo, mediaInfoKey, type MediaInfo } from '@/features/viewer/api'
import { api } from '@/lib/api'
import { isPublicScope } from '@/lib/public-scope'
import { formatDuration } from '@/lib/format'
import { getMediaUrl, isSignatureExpired } from '@/lib/media-url'
import { isShareScope } from '@/lib/share-scope'
import { pushScope, registerShortcut } from '@/lib/shortcuts'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/stores/session'
import { videoVerdict } from './codec'
import { canOfferTranscode, type HlsSource } from './hls'
import { HlsPlayer } from './HlsPlayer'
import { useHlsPlayback } from './useHlsPlayback'

/** Video speeds (SPEC §8.14: 0.25–2×; Video.js 10 ships a fixed list, so
 *  the menu is ours and calls `setPlaybackRate`). */
const VIDEO_RATES = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2] as const
/** Positions closer than this to either end are not offered for resume. */
const RESUME_MARGIN_S = 5
const SAVE_EVERY_MS = 5000

interface MediaPosition {
  path: string
  seconds: number
  duration?: number
  updatedAt: string | null
}

function savePosition(path: string, seconds: number, duration: number): void {
  if (isPublicScope()) return // guests have no resume store
  // near the end counts as watched: forget the position
  const done = duration > 0 && (seconds >= duration - RESUME_MARGIN_S || seconds / duration > 0.97)
  void api('/me/media-position', {
    method: 'PUT',
    body: {
      path,
      seconds: done || seconds < 1 ? 0 : seconds,
      ...(duration > 0 ? { duration } : {}),
    },
  }).catch(() => {})
}

export interface VideoPlayerProps {
  item: Item
  /** next video of the folder (auto-play when this one ends) */
  next: Item | null
  onNext: (item: Item) => void
  onDownload: () => void
}

/** Video viewer (SPEC §8.14 VideoPlayer, ADR-07): Video.js 10 store +
 *  `<Video>` with our own controls; codec check before loading, signed URL
 *  refresh on 403, sidecar subtitles, resume prompt, auto-next. Lazy chunk.
 *  A video the browser cannot decode can be converted to HLS on the server
 *  when the admin enabled it (Step 23d, `HlsPlayer`). */
export function VideoPlayer({ item, next, onNext, onDownload }: VideoPlayerProps) {
  const { t } = useTranslation('viewer')
  const transcodeEnabled = useSessionStore((s) => s.config?.transcodeEnabled)
  const [convert, setConvert] = useState<string | null>(null)
  const offer = (playable: boolean, decodeFailed = false) =>
    canOfferTranscode({
      verdict: { playable },
      decodeFailed,
      transcodeEnabled,
      foreignScope: isPublicScope() || isShareScope(),
    })
  const info = useQuery({
    queryKey: mediaInfoKey(item.path, false),
    queryFn: ({ signal }) => getMediaInfo(item.path, false, signal),
    staleTime: 60_000,
  })
  const [src, setSrc] = useState<string | null>(null)
  const [srcError, setSrcError] = useState(false)
  useEffect(() => {
    let alive = true
    getMediaUrl(item.path, 'media').then(
      (u) => alive && setSrc(u),
      () => alive && setSrcError(true),
    )
    return () => {
      alive = false
    }
  }, [item.path])
  const position = useQuery({
    queryKey: ['media-position', item.path],
    queryFn: () =>
      isPublicScope()
        ? Promise.resolve({ seconds: 0, updatedAt: null } as unknown as MediaPosition)
        : api<MediaPosition>('/me/media-position', { query: { path: item.path } }),
    staleTime: 0,
    gcTime: 0,
  })

  if (info.isPending || position.isPending || (!src && !srcError)) {
    return (
      <div
        className="flex h-full items-center justify-center text-sm text-text-muted"
        aria-busy="true"
      >
        {t('video.loading')}
      </div>
    )
  }
  const facts = info.data
  const probe = document.createElement('video')
  const verdict = videoVerdict(
    {
      name: item.name,
      mime: item.mime,
      codecVideo: facts?.codecVideo,
      codecAudio: facts?.codecAudio,
    },
    (type) => probe.canPlayType(type),
  )
  const body = (hls?: HlsSource) => (
    <Player>
      <PlayerBody
        key={item.path}
        item={item}
        src={src ?? ''}
        hls={hls}
        info={facts}
        // seeking beyond the converted part is impossible: resume only once
        // the conversion is complete
        resumeAt={hls && !hls.finished ? 0 : (position.data?.seconds ?? 0)}
        next={next}
        onNext={onNext}
        onDownload={onDownload}
        onConvert={!hls && offer(false, true) ? () => setConvert(item.path) : undefined}
      />
    </Player>
  )
  if (convert === item.path && !srcError) {
    return (
      <HlsPlayer item={item} onDownload={onDownload} onBack={() => setConvert(null)}>
        {(hls) => body(hls)}
      </HlsPlayer>
    )
  }
  if (!verdict.playable || srcError || !src) {
    return (
      <Unsupported
        message={
          !verdict.playable
            ? verdict.reason === 'codec'
              ? t('video.unsupportedCodec', { codec: verdict.label })
              : t('video.unsupportedContainer', { format: verdict.label })
            : t('video.failed')
        }
        onDownload={onDownload}
        onConvert={!srcError && offer(verdict.playable) ? () => setConvert(item.path) : undefined}
      />
    )
  }
  return body()
}

function Unsupported({
  message,
  onDownload,
  onConvert,
}: {
  message: string
  onDownload: () => void
  /** Step 23d: offered when the admin enabled transcoding */
  onConvert?: (() => void) | undefined
}) {
  const { t } = useTranslation('viewer')
  return (
    <div className="flex h-full items-center justify-center p-6" data-testid="video-unsupported">
      <div className="flex max-w-md flex-col items-center gap-3 rounded-lg border border-border bg-surface p-6 text-center">
        <p className="text-sm text-text" role="alert">
          {message}
        </p>
        <p className="text-xs text-text-muted">
          {onConvert ? t('video.transcodeOffer') : t('video.unsupportedHint')}
        </p>
        <div className="flex flex-wrap justify-center gap-2">
          {onConvert ? (
            <Button variant="primary" onClick={onConvert} data-testid="video-transcode">
              <Film aria-hidden="true" />
              {t('video.transcode')}
            </Button>
          ) : null}
          <Button
            variant={onConvert ? 'secondary' : 'primary'}
            onClick={onDownload}
            data-testid="video-download"
          >
            <Download aria-hidden="true" />
            {t('card.download')}
          </Button>
        </div>
      </div>
    </div>
  )
}

interface BodyProps {
  item: Item
  /** signed raw URL (direct playback; poster in both modes) */
  src: string
  /** Step 23d: play this HLS conversion instead of the raw file */
  hls?: HlsSource | undefined
  info: MediaInfo | undefined
  resumeAt: number
  next: Item | null
  onNext: (item: Item) => void
  onDownload: () => void
  /** decode failure → offer the conversion (when enabled) */
  onConvert?: (() => void) | undefined
}

function PlayerBody({
  item,
  src: initialSrc,
  hls,
  info,
  resumeAt,
  next,
  onNext,
  onDownload,
  onConvert,
}: BodyProps) {
  const { t } = useTranslation('viewer')
  const videoRef = useRef<HTMLVideoElement>(null)
  const [src, setSrc] = useState(initialSrc)
  const [failed, setFailed] = useState(false)
  const hlsPlayback = useHlsPlayback(videoRef, hls, () => setFailed(true))
  const [autoNext, setAutoNext] = useState(true)
  const [prompt, setPrompt] = useState(resumeAt >= RESUME_MARGIN_S)
  const restoreAt = useRef<number | null>(null)
  const refreshing = useRef(false)
  const store = usePlayer()
  const poster =
    initialSrc && info?.thumbState !== 'unsupported' ? `${initialSrc}&variant=thumb` : undefined

  // --- signed URL refresh (TTL 2 h) and decode failures ---
  const onError = useCallback(async () => {
    const v = videoRef.current
    if (!v || refreshing.current) return
    const at = v.currentTime
    const wasPlaying = !v.paused
    try {
      refreshing.current = true
      const res = await fetch(v.currentSrc || src, {
        headers: { Range: 'bytes=0-0' },
        credentials: 'omit',
      })
      if (await isSignatureExpired(res)) {
        const fresh = await getMediaUrl(item.path, 'media', { force: true })
        restoreAt.current = at
        if (wasPlaying || at > 0) v.dataset.resume = wasPlaying ? 'play' : 'pause'
        setSrc(fresh)
        return
      }
    } catch {
      /* network failure → message */
    } finally {
      refreshing.current = false
    }
    const code = v.error?.code
    if (
      code === MediaError.MEDIA_ERR_SRC_NOT_SUPPORTED ||
      code === MediaError.MEDIA_ERR_DECODE ||
      code === MediaError.MEDIA_ERR_NETWORK
    ) {
      setFailed(true)
    }
  }, [item.path, src])

  const onLoadedMetadata = useCallback(() => {
    const v = videoRef.current
    if (!v) return
    if (restoreAt.current !== null) {
      v.currentTime = restoreAt.current
      restoreAt.current = null
      if (v.dataset.resume === 'play') void v.play().catch(() => {})
      delete v.dataset.resume
    }
  }, [])

  // --- position: saved while playing, on pause and when leaving ---
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    let last = 0
    const tick = () => {
      if (Date.now() - last < SAVE_EVERY_MS || v.paused) return
      last = Date.now()
      savePosition(item.path, v.currentTime, v.duration)
    }
    const pause = () => {
      if (v.currentTime > 0 && !v.ended) savePosition(item.path, v.currentTime, v.duration)
    }
    v.addEventListener('timeupdate', tick)
    v.addEventListener('pause', pause)
    return () => {
      v.removeEventListener('timeupdate', tick)
      v.removeEventListener('pause', pause)
      if (v.currentTime > 0) savePosition(item.path, v.currentTime, v.duration)
    }
  }, [item.path])

  const onEnded = useCallback(() => {
    savePosition(item.path, 0, 0)
    if (autoNext && next) onNext(next)
  }, [autoNext, next, onNext, item.path])

  // --- shortcuts (SPEC §9.6 "Video"), scope above the viewer's ---
  useEffect(() => {
    const pop = pushScope('video')
    const st = () => store
    const seekBy = (d: number) => {
      const s = st()
      void s.seek(Math.max(0, Math.min(s.currentTime + d, s.duration || s.currentTime + d)))
    }
    const offs = [
      registerShortcut('videoPlay', () => {
        setPrompt(false)
        if (st().paused) void st().play()
        else st().pause()
      }),
      registerShortcut('videoBack', () => seekBy(-5)),
      registerShortcut('videoForward', () => seekBy(5)),
      registerShortcut('videoBack10', () => seekBy(-10)),
      registerShortcut('videoForward10', () => seekBy(10)),
      registerShortcut('videoMute', () => st().setMuted(!st().muted)),
      registerShortcut('videoVolumeUp', () => st().setVolume(Math.min(1, st().volume + 0.1))),
      registerShortcut('videoVolumeDown', () => st().setVolume(Math.max(0, st().volume - 0.1))),
      registerShortcut('videoFullscreen', () => {
        if (st().isFullscreen) void st().exitFullscreen()
        else void st().requestFullscreen()
      }),
      registerShortcut('videoPercent', (_e, keys) => {
        const d = st().duration
        if (d > 0) void st().seek((Number(keys) / 10) * d)
      }),
    ]
    return () => {
      offs.forEach((off) => off())
      pop()
    }
  }, [store])

  if (failed) {
    return (
      <Unsupported
        message={hls ? t('video.transcodeFailed') : t('video.failed')}
        onDownload={onDownload}
        onConvert={onConvert}
      />
    )
  }

  const subtitles = info?.subtitles ?? []
  return (
    <div
      className="flex h-full min-h-0 flex-col bg-black"
      data-testid="video-player"
      data-hls={hls ? (hlsPlayback.mode ?? 'pending') : undefined}
    >
      <Container className="group/player relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-black">
        <Video
          ref={videoRef}
          src={hls ? hlsPlayback.src : src}
          poster={poster}
          preload="metadata"
          playsInline
          className="max-h-full max-w-full"
          // HLS errors (expired folder signature, hls.js) are handled by useHlsPlayback
          onError={() => (hls ? undefined : void onError())}
          onLoadedMetadata={onLoadedMetadata}
          onEnded={onEnded}
          onPlay={() => setPrompt(false)}
          data-testid="video-element"
        >
          {subtitles.map((s, i) => (
            <track
              key={s.path}
              kind="subtitles"
              src={s.url}
              srcLang={s.lang || undefined}
              label={s.label}
              default={i === 0}
            />
          ))}
        </Video>
        {prompt ? (
          <ResumePrompt
            at={resumeAt}
            onResume={() => {
              setPrompt(false)
              void store.seek(resumeAt).then(() => store.play())
            }}
            onRestart={() => {
              setPrompt(false)
              void store.play()
            }}
          />
        ) : null}
        <Controls.Root>
          <Controls.Content className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/80 to-transparent px-3 pt-6 pb-2 text-white transition-opacity data-[visible]:opacity-100 [&:not([data-visible])]:opacity-0">
            <ControlBar
              subtitles={subtitles.length > 0}
              next={next}
              autoNext={autoNext}
              onAutoNext={setAutoNext}
              onNext={onNext}
            />
          </Controls.Content>
        </Controls.Root>
      </Container>
    </div>
  )
}

function ResumePrompt({
  at,
  onResume,
  onRestart,
}: {
  at: number
  onResume: () => void
  onRestart: () => void
}) {
  const { t } = useTranslation('viewer')
  return (
    <div
      className="absolute top-4 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-sm text-text shadow-lg"
      role="status"
      data-testid="video-resume"
    >
      <span>{t('video.resumeAt', { time: formatDuration(Math.floor(at)) })}</span>
      <Button size="sm" variant="primary" onClick={onResume} data-testid="video-resume-yes">
        {t('video.resume')}
      </Button>
      <Button size="sm" variant="ghost" onClick={onRestart} data-testid="video-resume-no">
        <RotateCcw aria-hidden="true" />
        {t('video.restart')}
      </Button>
    </div>
  )
}

const ctl =
  'inline-flex size-8 items-center justify-center rounded-md text-white/90 hover:bg-white/15 hover:text-white focus-visible:outline-2 focus-visible:outline-white disabled:opacity-40'

function ControlBar({
  subtitles,
  next,
  autoNext,
  onAutoNext,
  onNext,
}: {
  subtitles: boolean
  next: Item | null
  autoNext: boolean
  onAutoNext: (v: boolean) => void
  onNext: (item: Item) => void
}) {
  const { t } = useTranslation('viewer')
  const paused = usePlayer((s) => s.paused)
  const currentTime = usePlayer((s) => s.currentTime)
  const duration = usePlayer((s) => s.duration)
  const muted = usePlayer((s) => s.muted)
  const volume = usePlayer((s) => s.volume)
  const rate = usePlayer((s) => s.playbackRate)
  const isFullscreen = usePlayer((s) => s.isFullscreen)
  const pipAvailable = usePlayer((s) => s.pictureInPictureAvailability)
  const isPip = usePlayer((s) => s.isPictureInPicture)
  const tracks = usePlayer((s) => s.textTrackList)
  const store = usePlayer()
  const subtitleTracks = tracks.filter((tr) => tr.kind === 'subtitles' || tr.kind === 'captions')
  const showing = subtitleTracks.find((tr) => tr.mode === 'showing')

  return (
    <div className="flex flex-col gap-1">
      <TimeSlider.Root
        className="relative flex h-4 w-full cursor-pointer items-center"
        label={t('video.seek')}
        data-testid="video-seek"
      >
        <TimeSlider.Track className="relative h-1 w-full overflow-hidden rounded-full bg-white/30">
          <TimeSlider.Buffer className="absolute inset-y-0 left-0 w-[var(--media-slider-buffer)] bg-white/40" />
          <TimeSlider.Fill className="absolute inset-y-0 left-0 w-[var(--media-slider-fill)] bg-white" />
        </TimeSlider.Track>
        <TimeSlider.Thumb className="absolute left-[var(--media-slider-fill)] size-3 -translate-x-1/2 rounded-full bg-white shadow" />
      </TimeSlider.Root>
      <div className="flex items-center gap-1 text-xs">
        <button
          type="button"
          className={ctl}
          onClick={() => (paused ? void store.play() : store.pause())}
          aria-label={paused ? t('audio.play') : t('audio.pause')}
          title={`${paused ? t('audio.play') : t('audio.pause')} (Space)`}
          data-testid="video-toggle"
        >
          {paused ? (
            <Play aria-hidden="true" className="size-4" />
          ) : (
            <Pause aria-hidden="true" className="size-4" />
          )}
        </button>
        {next ? (
          <button
            type="button"
            className={ctl}
            onClick={() => onNext(next)}
            aria-label={t('video.nextVideo', { name: next.name })}
            title={t('video.nextVideo', { name: next.name })}
          >
            <SkipForward aria-hidden="true" className="size-4" />
          </button>
        ) : null}
        <button
          type="button"
          className={ctl}
          onClick={() => store.setMuted(!muted)}
          aria-label={muted ? t('audio.unmute') : t('audio.mute')}
          title={`${muted ? t('audio.unmute') : t('audio.mute')} (M)`}
        >
          {muted || volume === 0 ? (
            <VolumeX aria-hidden="true" className="size-4" />
          ) : (
            <Volume2 aria-hidden="true" className="size-4" />
          )}
        </button>
        <input
          type="range"
          min={0}
          max={1}
          step={0.05}
          value={muted ? 0 : volume}
          onChange={(e) => {
            store.setVolume(Number(e.target.value))
            if (muted) store.setMuted(false)
          }}
          aria-label={t('audio.volume')}
          className="fm-range hidden w-20 sm:block"
        />
        <span className="ml-1 tabular text-white/90" data-testid="video-time">
          {formatDuration(Math.floor(currentTime))} / {formatDuration(duration)}
        </span>
        <span className="flex-1" />
        {next ? (
          <label className="hidden items-center gap-1 text-white/90 md:flex">
            <input
              type="checkbox"
              checked={autoNext}
              onChange={(e) => onAutoNext(e.target.checked)}
            />
            {t('video.autoNext')}
          </label>
        ) : null}
        <select
          value={rate}
          onChange={(e) => store.setPlaybackRate(Number(e.target.value))}
          aria-label={t('audio.speed')}
          className="h-7 rounded-md border border-white/30 bg-black/60 px-1 text-xs text-white"
          data-testid="video-rate"
        >
          {VIDEO_RATES.map((r) => (
            <option key={r} value={r}>
              {r}×
            </option>
          ))}
        </select>
        {subtitles ? (
          <label className="flex items-center gap-1">
            <Captions aria-hidden="true" className="size-4 text-white/90" />
            <select
              value={showing?.id ?? ''}
              onChange={(e) => store.selectSubtitlesTrack(e.target.value || null)}
              aria-label={t('video.subtitles')}
              className="h-7 max-w-36 rounded-md border border-white/30 bg-black/60 px-1 text-xs text-white"
              data-testid="video-subtitles"
            >
              <option value="">{t('video.subtitlesOff')}</option>
              {subtitleTracks.map((tr) => (
                <option key={tr.id} value={tr.id}>
                  {tr.label || tr.language || tr.id}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        <button
          type="button"
          className={cn(ctl, isPip && 'bg-white/20')}
          onClick={() =>
            isPip
              ? void store.exitPictureInPicture()
              : void store.requestPictureInPicture().catch(() => {})
          }
          disabled={pipAvailable !== 'available'}
          aria-label={t('video.pip')}
          aria-pressed={isPip}
          title={t('video.pip')}
          data-testid="video-pip"
        >
          <PictureInPicture2 aria-hidden="true" className="size-4" />
        </button>
        <button
          type="button"
          className={ctl}
          onClick={() =>
            isFullscreen ? void store.exitFullscreen() : void store.requestFullscreen()
          }
          aria-label={isFullscreen ? t('image.exitFullscreen') : t('image.fullscreen')}
          title={`${isFullscreen ? t('image.exitFullscreen') : t('image.fullscreen')} (F)`}
          data-testid="video-fullscreen"
        >
          {isFullscreen ? (
            <Minimize aria-hidden="true" className="size-4" />
          ) : (
            <Maximize aria-hidden="true" className="size-4" />
          )}
        </button>
      </div>
    </div>
  )
}
