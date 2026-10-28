/** On-the-fly HLS transcoding (SPEC §8.14 "Transcoding HLS", Step 23d):
 *  API calls and the pure decisions of the player — whether to offer
 *  "play with conversion", native HLS vs hls.js, playlist readiness, and
 *  carrying a fresh folder signature into hls.js requests. */
import { api } from '@/lib/api'
import type { Task } from '@/features/tasks/api'
import type { Verdict } from './codec'

/** Signed playlist of a conversion handed to the player. */
export interface HlsSource {
  url: string
  /** ffmpeg finished when playback started (seeking anywhere works). */
  finished: boolean
  /** POST /files/hls again → a fresh signed playlist URL (same task). */
  resign: () => Promise<string>
}

export interface HlsStart {
  task: Task
  /** signed `/api/v1/files/hls/<task>/playlist.m3u8?scope&exp&sig` */
  playlistUrl: string
  expiresAt: string
  /** ≥ 2 segments exist or the conversion finished */
  ready: boolean
}

/** POST /files/hls: a new conversion (202) or the one of this file that
 *  runs or finished already (200, with a fresh signed URL). */
export function startTranscode(path: string): Promise<HlsStart> {
  return api<HlsStart>('/files/hls', { method: 'POST', body: { path } })
}

/** The "Putar dengan konversi (lambat)" button: only for a video this
 *  browser cannot play, when the admin enabled transcoding, and only for the
 *  owner's own files (no guest page, received share or admin browse). */
export function canOfferTranscode(opts: {
  verdict: Pick<Verdict, 'playable'> | null
  decodeFailed?: boolean
  transcodeEnabled: boolean | undefined
  foreignScope: boolean
}): boolean {
  if (opts.transcodeEnabled !== true || opts.foreignScope) return false
  return (opts.verdict !== null && !opts.verdict.playable) || opts.decodeFailed === true
}

export type HlsMode = 'native' | 'hlsjs' | 'none'

/** Safari/iOS (WebKit: `navigator.vendor` Apple) play the playlist natively
 *  in `<video src>` (Apple's recommendation; iOS < 17.1 has no MSE).
 *  Everywhere else hls.js feeds Media Source Extensions, even when the
 *  browser answers "maybe" itself (Chromium ≥ 141 has a built-in HLS
 *  player): one code path for EXT-X-START, live reloads and re-signing.
 *  Without MSE a native player is still used; neither → "cannot play". */
export function hlsPlaybackMode(caps: {
  nativeHls: string
  mse: boolean
  apple: boolean
}): HlsMode {
  const native = caps.nativeHls === 'probably' || caps.nativeHls === 'maybe'
  if (native && caps.apple) return 'native'
  if (caps.mse) return 'hlsjs'
  if (native) return 'native'
  return 'none'
}

/** Browser capabilities for hlsPlaybackMode (H.264/AAC through MSE). */
export function detectHlsCaps(video: Pick<HTMLVideoElement, 'canPlayType'>): {
  nativeHls: string
  mse: boolean
  apple: boolean
} {
  const w = globalThis as unknown as {
    MediaSource?: { isTypeSupported?: (t: string) => boolean }
    ManagedMediaSource?: { isTypeSupported?: (t: string) => boolean }
    navigator?: { vendor?: string }
  }
  const ms = w.MediaSource ?? w.ManagedMediaSource
  const mse = !!ms?.isTypeSupported?.('video/mp4; codecs="avc1.4d401f,mp4a.40.2"')
  const apple = /^Apple/.test(w.navigator?.vendor ?? '')
  return { nativeHls: video.canPlayType('application/vnd.apple.mpegurl'), mse, apple }
}

export interface PlaylistState {
  segments: number
  ended: boolean
  ready: boolean
}

/** Readiness of an EVENT playlist: playback starts once 2 segments exist
 *  (8 s of video), or with whatever exists once ffmpeg ended the list. */
export function playlistReadiness(text: string): PlaylistState {
  if (!text.startsWith('#EXTM3U')) return { segments: 0, ended: false, ready: false }
  const lines = text.split(/\r?\n/)
  const segments = lines.filter((l) => l.startsWith('#EXTINF:')).length
  const ended = lines.some((l) => l.trim() === '#EXT-X-ENDLIST')
  return { segments, ended, ready: ended ? segments > 0 : segments >= 2 }
}

/** Replaces the folder signature (scope/exp/sig) of a playlist or segment
 *  URL with the one of `fresh` — every file of a task shares one HMAC, so a
 *  re-signed playlist URL also renews the segments hls.js asks for. */
export function withSignature(url: string, fresh: string): string {
  const base = typeof window === 'undefined' ? 'http://localhost' : window.location.origin
  const target = new URL(url, base)
  const source = new URL(fresh, base)
  for (const k of ['scope', 'exp', 'sig']) {
    const v = source.searchParams.get(k)
    if (v !== null) target.searchParams.set(k, v)
  }
  return target.origin === base ? target.pathname + target.search : target.toString()
}
