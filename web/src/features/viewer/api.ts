/** Viewer API client (SPEC §6.3 `GET /files/media-info`). */
import { api } from '@/lib/api'
import { isPublicScope, publicApiPath } from '@/lib/public-scope'

export interface ExifInfo {
  make?: string
  model?: string
  lens?: string
  software?: string
  dateTimeOriginal?: string
  dateTime?: string
  exposureTime?: string
  fNumber?: number
  iso?: number
  focalLength?: number
  focalLength35?: number
  orientation?: number
  flash?: boolean
  width?: number
  height?: number
  /** A position is stored; ask again with `includeGps` to receive it. */
  hasGps: boolean
}

export interface GpsInfo {
  lat: number
  lon: number
  alt?: number
}

/** Sidecar subtitle of a video (Step 17b; signed `/files/subtitle` URL). */
export interface SubtitleTrack {
  path: string
  name: string
  lang?: string
  label: string
  format: 'vtt' | 'srt'
  url: string
}

export type MediaKind = 'image' | 'svg' | 'video' | 'audio' | 'pdf' | 'other'

export interface MediaInfo {
  path: string
  name: string
  size: number
  mtime: string
  category: string
  mime: string
  kind: MediaKind
  width?: number
  height?: number
  durationMs?: number
  codecVideo?: string
  codecAudio?: string
  bitrate?: number
  hasCover: boolean
  tags?: Record<string, string>
  thumbState?: 'pending' | 'ready' | 'failed' | 'unsupported'
  exif: ExifInfo | null
  /** Only present when requested with `includeGps` and stored in the file. */
  gps?: GpsInfo
  /** Videos only: sidecar .vtt/.srt files of the same folder. */
  subtitles?: SubtitleTrack[]
}

export function getMediaInfo(
  path: string,
  includeGps = false,
  signal?: AbortSignal,
): Promise<MediaInfo> {
  const query = { path, include_gps: includeGps ? 1 : undefined }
  const route = isPublicScope() ? publicApiPath('media-info') : '/files/media-info'
  return api<MediaInfo>(route, signal ? { query, signal } : { query })
}

export const mediaInfoKey = (path: string, includeGps: boolean) =>
  ['media-info', path, includeGps] as const
