/** Can this browser play a video? (SPEC §8.14 VideoPlayer, PROMPT 17b task 3)
 *  `canPlayType` with the container and — once the server probed the file —
 *  the real codecs from `media-info` (ffprobe names). No transcoding exists
 *  (23d), so a "no" becomes a clear message + download instead of a black
 *  screen. Pure, so it is unit-tested with a fake `canPlayType`. */

export interface VideoFacts {
  name: string
  mime?: string | null | undefined
  /** ffprobe codec_name, e.g. h264, hevc, vp9, av1 */
  codecVideo?: string | undefined
  /** ffprobe codec_name, e.g. aac, opus, mp3 */
  codecAudio?: string | undefined
}

export type CanPlayType = (type: string) => string

export type Verdict =
  | { playable: true; type: string }
  | { playable: false; reason: 'container' | 'codec'; label: string }

const CONTAINERS: Record<string, string> = {
  mp4: 'video/mp4',
  m4v: 'video/mp4',
  mov: 'video/quicktime',
  webm: 'video/webm',
  ogv: 'video/ogg',
  ogg: 'video/ogg',
  mkv: 'video/x-matroska',
  avi: 'video/x-msvideo',
  wmv: 'video/x-ms-wmv',
  flv: 'video/x-flv',
  mpg: 'video/mpeg',
  mpeg: 'video/mpeg',
  ts: 'video/mp2t',
  m2ts: 'video/mp2t',
  '3gp': 'video/3gpp',
}

/** ffprobe name → RFC 6381 codec string for canPlayType. */
const VIDEO_CODECS: Record<string, string> = {
  h264: 'avc1.42E01E',
  hevc: 'hvc1.1.6.L93.B0',
  h265: 'hvc1.1.6.L93.B0',
  vp8: 'vp8',
  vp9: 'vp09.00.10.08',
  av1: 'av01.0.05M.08',
  theora: 'theora',
  mpeg4: 'mp4v.20.8',
}
const AUDIO_CODECS: Record<string, string> = {
  aac: 'mp4a.40.2',
  mp3: 'mp4a.6B',
  opus: 'opus',
  vorbis: 'vorbis',
  flac: 'flac',
  ac3: 'ac-3',
  eac3: 'ec-3',
}

const LABELS: Record<string, string> = {
  h264: 'H.264',
  hevc: 'HEVC (H.265)',
  h265: 'HEVC (H.265)',
  vp8: 'VP8',
  vp9: 'VP9',
  av1: 'AV1',
  mpeg4: 'MPEG-4 Part 2',
  mpeg2video: 'MPEG-2',
  mpeg1video: 'MPEG-1',
  msmpeg4v3: 'MS-MPEG4',
  wmv3: 'WMV',
  vc1: 'VC-1',
  theora: 'Theora',
  prores: 'ProRes',
  aac: 'AAC',
  mp3: 'MP3',
  opus: 'Opus',
  vorbis: 'Vorbis',
  flac: 'FLAC',
  ac3: 'AC-3',
  eac3: 'E-AC-3',
  dts: 'DTS',
  truehd: 'TrueHD',
}

export function codecLabel(codec: string): string {
  return LABELS[codec.toLowerCase()] ?? codec.toUpperCase()
}

function extOf(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : ''
}

export function containerType(f: Pick<VideoFacts, 'name' | 'mime'>): string {
  return CONTAINERS[extOf(f.name)] ?? (f.mime?.startsWith('video/') ? f.mime : 'video/mp4')
}

/** The verdict for `f`. Unknown codecs (not probed yet) only test the
 *  container; the player still falls back to the message when the element
 *  reports a decode error. */
export function videoVerdict(f: VideoFacts, canPlayType: CanPlayType): Verdict {
  let container = containerType(f)
  // QuickTime files are ISO-BMFF: Chromium plays them as MP4 although it
  // answers "" for video/quicktime
  if (container === 'video/quicktime' && canPlayType(container) === '') container = 'video/mp4'
  const cv = f.codecVideo?.toLowerCase()
  const ca = f.codecAudio?.toLowerCase()
  if (canPlayType(container) === '') {
    return { playable: false, reason: 'container', label: extOf(f.name).toUpperCase() || container }
  }
  if (cv) {
    const v = VIDEO_CODECS[cv]
    if (!v) return { playable: false, reason: 'codec', label: codecLabel(cv) }
    if (canPlayType(`${container}; codecs="${v}"`) === '') {
      return { playable: false, reason: 'codec', label: codecLabel(cv) }
    }
  }
  if (ca) {
    const a = AUDIO_CODECS[ca]
    // an unplayable audio track would leave a silent video: still refuse,
    // the user downloads it instead
    if (!a || canPlayType(`${container}; codecs="${a}"`) === '') {
      return { playable: false, reason: 'codec', label: codecLabel(ca) }
    }
  }
  const codecs = [cv && VIDEO_CODECS[cv], ca && AUDIO_CODECS[ca]].filter(Boolean).join(',')
  return { playable: true, type: codecs ? `${container}; codecs="${codecs}"` : container }
}
