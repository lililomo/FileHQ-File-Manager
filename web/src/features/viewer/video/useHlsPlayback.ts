import { useEffect, useMemo, useRef, useState, type RefObject } from 'react'
import { isSignatureExpired } from '@/lib/media-url'
import { detectHlsCaps, hlsPlaybackMode, withSignature, type HlsMode, type HlsSource } from './hls'

/** Minimal surface of hls.js used here (the module is imported lazily). */
interface HlsLike {
  loadSource(url: string): void
  attachMedia(media: HTMLMediaElement): void
  recoverMediaError(): void
  destroy(): void
  on(event: string, cb: (event: string, data: HlsErrorData) => void): void
}
interface HlsErrorData {
  fatal: boolean
  type: string
  details: string
  response?: { code: number }
}

/** Attaches an HLS source to the player's `<video>`: native playback
 *  (Safari/iOS) through `src`, otherwise hls.js loaded on demand (its own
 *  chunk). Requests carry the newest folder signature; a 403
 *  SIGNATURE_EXPIRED re-signs through POST /files/hls and resumes at the
 *  same position, like the raw player does. Returns the `src` for native
 *  mode (undefined while hls.js drives the element). */
export function useHlsPlayback(
  videoRef: RefObject<HTMLVideoElement | null>,
  source: HlsSource | undefined,
  onFailed: () => void,
): { src: string | undefined; mode: HlsMode | null } {
  // decided once per player from a probe element (like the codec check)
  const mode = useMemo<HlsMode | null>(
    () => (source ? hlsPlaybackMode(detectHlsCaps(document.createElement('video'))) : null),
    [source],
  )
  const [nativeSrc, setNativeSrc] = useState<string | undefined>(source?.url)
  const failRef = useRef(onFailed)
  useEffect(() => {
    failRef.current = onFailed
  })
  const initialUrl = source?.url
  const resign = source?.resign
  useEffect(() => {
    const v = videoRef.current
    if (!initialUrl || !resign || !v || !mode) return
    const m = mode
    if (m === 'none') {
      failRef.current()
      return
    }
    let current = initialUrl
    let alive = true
    let refreshing = false
    /** true when the playlist answers SIGNATURE_EXPIRED (then `current` is renewed) */
    const renewIfExpired = async (): Promise<boolean> => {
      const res = await fetch(current, { credentials: 'omit' })
      if (!(await isSignatureExpired(res))) return false
      current = await resign()
      return alive
    }
    if (m === 'native') {
      const onError = () => {
        if (refreshing) return
        refreshing = true
        const at = v.currentTime
        const playing = !v.paused
        void renewIfExpired()
          .then((renewed) => {
            if (!renewed) {
              failRef.current()
              return
            }
            v.addEventListener(
              'loadedmetadata',
              () => {
                v.currentTime = at
                if (playing) void v.play().catch(() => {})
              },
              { once: true },
            )
            setNativeSrc(current)
          })
          .catch(() => failRef.current())
          .finally(() => {
            refreshing = false
          })
      }
      v.addEventListener('error', onError)
      return () => {
        alive = false
        v.removeEventListener('error', onError)
      }
    }
    // hls.js (lazy chunk)
    let hls: HlsLike | null = null
    let recovered = false
    const create = async (startAt: number, play: boolean) => {
      const mod = await import('hls.js/light')
      const Hls = mod.default
      if (!alive) return
      if (!Hls.isSupported()) {
        failRef.current()
        return
      }
      hls?.destroy()
      const h = new Hls({
        startPosition: startAt,
        // every file of the task shares one signature: always send the newest
        xhrSetup: (xhr: XMLHttpRequest, u: string) =>
          xhr.open('GET', withSignature(u, current), true),
      }) as unknown as HlsLike
      hls = h
      h.on(Hls.Events.ERROR, (_e, data) => {
        if (data.response?.code === 403) {
          if (refreshing) return
          refreshing = true
          const at = v.currentTime
          const playing = !v.paused
          void renewIfExpired()
            .then((renewed) => {
              if (renewed) {
                if (data.fatal) void create(at, playing)
              } else if (data.fatal) failRef.current()
            })
            .catch(() => data.fatal && failRef.current())
            .finally(() => {
              refreshing = false
            })
          return
        }
        if (!data.fatal) return
        if (data.type === Hls.ErrorTypes.MEDIA_ERROR && !recovered) {
          recovered = true
          h.recoverMediaError()
          return
        }
        failRef.current()
      })
      h.loadSource(current)
      h.attachMedia(v)
      if (play)
        h.on(Hls.Events.MANIFEST_PARSED, () => {
          void v.play().catch(() => {})
        })
    }
    void create(0, false).catch(() => failRef.current())
    return () => {
      alive = false
      hls?.destroy()
      hls = null
    }
  }, [videoRef, initialUrl, resign, mode])
  return { src: mode === 'native' ? nativeSrc : undefined, mode }
}
