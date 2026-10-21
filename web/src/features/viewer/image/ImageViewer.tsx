import {
  Expand,
  FlipHorizontal2,
  FlipVertical2,
  Loader2,
  Maximize2,
  Minimize2,
  Pause,
  Play,
  RotateCcw,
  RotateCw,
  Scan,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { Item } from '@/features/browser/api'
import { getMediaUrl } from '@/lib/media-url'
import { registerShortcut } from '@/lib/shortcuts'
import { cn } from '@/lib/utils'
import { usePref, usePrefsStore } from '@/stores/prefs'
import { imageSourceFor, viewerFor } from '../viewers'
import { previewUrl, SLIDESHOW_INTERVALS } from './source'
import { useZoomPan } from './useZoomPan'
import { fitScale, transformOf, zoomPercent, type Box } from './zoomPan'

export interface ImageViewerProps {
  item: Item
  /** position among the sibling images (for the slideshow wrap-around) */
  index: number
  total: number
  onGoTo: (index: number) => void
  /** next sibling, preloaded while this one is shown */
  nextItem?: Item | null | undefined
  onDownload: () => void
  /** Quick Look: no slideshow, Space is taken by the overlay. */
  slideshow?: boolean
}

type Status = 'loading' | 'ready' | 'error'

/** Image viewer (SPEC §8.14 ImageViewer, PROMPT 17a task 2): the 2048 px
 *  preview is shown first, the original replaces it at ≥ 100 % (or 1:1) for
 *  browser-decodable formats; GIFs come from the original so they animate;
 *  SVG/TIFF/HEIC only ever use the raster preview. Zoom/pan/rotate/flip are
 *  display transforms (the file is never written). Per-file state lives in
 *  the keyed canvas so switching files starts from scratch. */
export function ImageViewer(props: ImageViewerProps) {
  // the slideshow outlives the per-file canvas (it advances across files)
  const [playRequested, setPlayRequested] = useState(false)
  const togglePlay = useCallback(() => setPlayRequested((p) => !p), [])
  return (
    <ImageCanvas
      key={`${props.item.path}\u0000${props.item.etag}`}
      {...props}
      playRequested={playRequested}
      onTogglePlay={togglePlay}
    />
  )
}

function ImageCanvas({
  item,
  index,
  total,
  onGoTo,
  nextItem,
  onDownload,
  slideshow = true,
  playRequested,
  onTogglePlay,
}: ImageViewerProps & { playRequested: boolean; onTogglePlay: () => void }) {
  const { t } = useTranslation('viewer')
  const source = imageSourceFor(item)
  const [status, setStatus] = useState<Status>('loading')
  const [src, setSrc] = useState<string | null>(null)
  const [signed, setSigned] = useState<string | null>(null)
  const [previewDims, setPreviewDims] = useState<Box | null>(null)
  // true once the original bytes are what the <img> shows
  const [original, setOriginal] = useState(source === 'animated')
  const originalStarted = useRef(false)
  const retried = useRef(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const [fullscreen, setFullscreen] = useState(false)

  // the <img> box is the original size so scale 1 = 1:1 whatever is loaded
  const mw = item.media?.width ?? 0
  const mh = item.media?.height ?? 0
  const known = useMemo<Box | null>(() => (mw > 0 && mh > 0 ? { w: mw, h: mh } : null), [mw, mh])
  const natural: Box | null = known ?? previewDims
  const { viewportRef, viewport, state, dispatch, bind, dragging } = useZoomPan(natural)

  // --- source selection (async only; the keyed mount already reset state) ---
  useEffect(() => {
    let cancelled = false
    getMediaUrl(item.path, 'media')
      .then((url) => {
        if (cancelled) return
        setSigned(url)
        setSrc(source === 'animated' ? url : previewUrl(url))
      })
      .catch(() => {
        if (!cancelled) setStatus('error')
      })
    return () => {
      cancelled = true
    }
  }, [item.path, source])

  // original at ≥ 100 % for direct formats (preloaded, then swapped in). The
  // initial state is scale 1 in fit mode until the first layout runs, so fit
  // mode only counts once the fitted scale really is 1 (small photos).
  const fitted = natural ? fitScale(viewport, natural, state.rotate, 'fit') : 0
  const wantOriginal =
    source === 'direct' &&
    status === 'ready' &&
    !!natural &&
    viewport.w > 0 &&
    state.scale >= 1 &&
    (state.mode === 'custom' || fitted >= 1)
  useEffect(() => {
    if (!wantOriginal || original || !signed || originalStarted.current) return
    originalStarted.current = true
    const im = new Image()
    im.decoding = 'async'
    im.onload = () => {
      setSrc(signed)
      setOriginal(true)
    }
    im.onerror = () => {
      originalStarted.current = false
    }
    im.src = signed
  }, [wantOriginal, original, signed])

  // preload the next sibling's preview (SPEC §8.14)
  useEffect(() => {
    if (!nextItem || viewerFor(nextItem) !== 'image') return
    let cancelled = false
    getMediaUrl(nextItem.path, 'media')
      .then((url) => {
        if (cancelled) return
        const im = new Image()
        im.src = imageSourceFor(nextItem) === 'animated' ? url : previewUrl(url)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [nextItem])

  const onLoad = useCallback(
    (e: React.SyntheticEvent<HTMLImageElement>) => {
      const el = e.currentTarget
      if (!known && el.naturalWidth > 0)
        setPreviewDims((d) => d ?? { w: el.naturalWidth, h: el.naturalHeight })
      setStatus('ready')
    },
    [known],
  )
  const reload = useCallback(() => {
    setStatus('loading')
    getMediaUrl(item.path, 'media', { force: true })
      .then((url) => {
        setSigned(url)
        setSrc(source === 'animated' ? url : previewUrl(url))
      })
      .catch(() => setStatus('error'))
  }, [item.path, source])
  const onError = useCallback(() => {
    if (!retried.current) {
      // expired signature or a preview that was still rendering: re-sign once
      retried.current = true
      setTimeout(reload, 1500)
      return
    }
    if (source === 'direct' && signed && src !== signed) {
      // no preview for this file: fall back to the original
      setSrc(signed)
      setOriginal(true)
      return
    }
    setStatus('error')
  }, [reload, source, signed, src])

  // --- fullscreen ---
  useEffect(() => {
    const onChange = () => setFullscreen(document.fullscreenElement === rootRef.current)
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])
  const toggleFullscreen = useCallback(() => {
    const el = rootRef.current
    if (!el) return
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => {})
    else void el.requestFullscreen?.().catch(() => {})
  }, [])

  // --- slideshow ---
  const interval = usePref<number>('viewer.slideshow_interval', 5)
  const setPref = usePrefsStore((s) => s.set)
  const playing = slideshow && playRequested && total >= 2
  useEffect(() => {
    if (!playing) return
    const id = setTimeout(() => onGoTo((index + 1) % total), interval * 1000)
    return () => clearTimeout(id)
  }, [playing, interval, index, total, onGoTo])

  // --- shortcuts (viewer scope; prev/next/info belong to the overlay) ---
  useEffect(() => {
    const offs = [
      registerShortcut('viewerZoomIn', () => dispatch({ type: 'zoomIn' })),
      registerShortcut('viewerZoomOut', () => dispatch({ type: 'zoomOut' })),
      registerShortcut('viewerFit', () => dispatch({ type: 'fit' })),
      registerShortcut('viewerOriginal', () => dispatch({ type: 'original' })),
      registerShortcut('viewerRotate', () => dispatch({ type: 'rotate', dir: 1 })),
      registerShortcut('viewerRotateLeft', () => dispatch({ type: 'rotate', dir: -1 })),
      registerShortcut('viewerFullscreen', toggleFullscreen),
      ...(slideshow ? [registerShortcut('viewerSlideshow', onTogglePlay)] : []),
    ]
    return () => offs.forEach((off) => off())
  }, [dispatch, toggleFullscreen, slideshow, onTogglePlay])

  const percent = zoomPercent(state)
  const transform = transformOf(state)
  const loadingOriginal = wantOriginal && !original

  return (
    <div ref={rootRef} className="flex h-full min-h-0 flex-col bg-bg" data-testid="image-viewer">
      <div
        className="flex h-10 shrink-0 items-center gap-0.5 border-b border-border bg-surface px-2"
        role="toolbar"
        aria-label={t('image.toolbar')}
      >
        <IconButton
          label={t('image.zoomOut')}
          shortcut="-"
          onClick={() => dispatch({ type: 'zoomOut' })}
        >
          <ZoomOut aria-hidden="true" />
        </IconButton>
        <Button
          variant="ghost"
          size="sm"
          className="w-16 tabular"
          onClick={() => dispatch({ type: 'fit' })}
          aria-label={t('image.zoomLevel', { percent })}
          data-testid="viewer-zoom"
        >
          {percent}%
        </Button>
        <IconButton
          label={t('image.zoomIn')}
          shortcut="+"
          onClick={() => dispatch({ type: 'zoomIn' })}
        >
          <ZoomIn aria-hidden="true" />
        </IconButton>
        <IconButton
          label={t('image.original')}
          shortcut="1"
          onClick={() => dispatch({ type: 'original' })}
          data-testid="viewer-original"
        >
          <Scan aria-hidden="true" />
        </IconButton>
        <IconButton label={t('image.fit')} shortcut="0" onClick={() => dispatch({ type: 'fit' })}>
          <Minimize2 aria-hidden="true" />
        </IconButton>
        <IconButton label={t('image.fill')} onClick={() => dispatch({ type: 'fill' })}>
          <Expand aria-hidden="true" />
        </IconButton>
        <span className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
        <IconButton
          label={t('image.rotateLeft')}
          shortcut="Shift+R"
          onClick={() => dispatch({ type: 'rotate', dir: -1 })}
        >
          <RotateCcw aria-hidden="true" />
        </IconButton>
        <IconButton
          label={t('image.rotateRight')}
          shortcut="R"
          onClick={() => dispatch({ type: 'rotate', dir: 1 })}
          data-testid="viewer-rotate"
        >
          <RotateCw aria-hidden="true" />
        </IconButton>
        <IconButton label={t('image.flipH')} onClick={() => dispatch({ type: 'flipH' })}>
          <FlipHorizontal2 aria-hidden="true" />
        </IconButton>
        <IconButton label={t('image.flipV')} onClick={() => dispatch({ type: 'flipV' })}>
          <FlipVertical2 aria-hidden="true" />
        </IconButton>
        <span className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
        {slideshow ? (
          <>
            <IconButton
              label={playing ? t('image.slideshowPause') : t('image.slideshowPlay')}
              shortcut="Space"
              onClick={onTogglePlay}
              disabled={total < 2}
              aria-pressed={playing}
              data-testid="viewer-slideshow"
            >
              {playing ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}
            </IconButton>
            <Select
              value={String(interval)}
              onValueChange={(v) => setPref('viewer.slideshow_interval', Number(v))}
            >
              <SelectTrigger
                size="sm"
                className="h-7 w-[92px] text-xs"
                aria-label={t('image.interval')}
                data-testid="viewer-interval"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SLIDESHOW_INTERVALS.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {t('image.intervalSeconds', { n })}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </>
        ) : null}
        <span className="flex-1" />
        {loadingOriginal ? (
          <span className="flex items-center gap-1 text-xs text-text-muted" aria-live="polite">
            <Loader2 className="size-3 animate-spin" aria-hidden="true" />
            {t('image.loadingOriginal')}
          </span>
        ) : null}
        <IconButton
          label={fullscreen ? t('image.exitFullscreen') : t('image.fullscreen')}
          shortcut="F"
          onClick={toggleFullscreen}
        >
          <Maximize2 aria-hidden="true" />
        </IconButton>
      </div>
      <div
        ref={viewportRef}
        className={cn(
          'relative min-h-0 flex-1 touch-none overflow-hidden select-none',
          dragging ? 'cursor-grabbing' : 'cursor-grab',
        )}
        style={{ background: 'var(--surface-2)' }}
        aria-label={t('image.canvas')}
        data-testid="viewer-canvas"
        data-source={original ? 'original' : 'preview'}
        data-status={status}
        {...bind}
      >
        {status === 'error' ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-sm text-text-muted">
            <p role="alert">{t('image.error')}</p>
            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  retried.current = false
                  reload()
                }}
              >
                {t('image.retry')}
              </Button>
              <Button variant="outline" size="sm" onClick={onDownload}>
                {t('header.download')}
              </Button>
            </div>
          </div>
        ) : (
          <>
            {status === 'loading' ? (
              <div
                className="absolute inset-0 flex items-center justify-center text-sm text-text-muted"
                aria-busy="true"
              >
                <Loader2 className="mr-2 size-4 animate-spin" aria-hidden="true" />
                {t('image.loading')}
              </div>
            ) : null}
            {src ? (
              <img
                src={src}
                alt={item.name}
                draggable={false}
                decoding="async"
                onLoad={onLoad}
                onError={onError}
                className="absolute top-1/2 left-1/2 max-w-none"
                style={{
                  width: natural?.w,
                  height: natural?.h,
                  marginLeft: natural ? -natural.w / 2 : 0,
                  marginTop: natural ? -natural.h / 2 : 0,
                  transform,
                  transformOrigin: 'center',
                  visibility: status === 'ready' && natural ? 'visible' : 'hidden',
                  imageRendering: state.scale > 2 ? 'pixelated' : 'auto',
                }}
                data-testid="viewer-image"
              />
            ) : null}
          </>
        )}
      </div>
    </div>
  )
}
