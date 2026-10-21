/** MIME/extension → viewer registry (SPEC §8.14 "Pemetaan MIME → viewer").
 *  Step 17a implements `image` and `pdf`; the other kinds are mapped already
 *  so navigation groups files of a kind together, but render the info card
 *  until Step 17b adds their viewers. */
import type { Item } from '@/features/browser/api'

export type ViewerKind =
  'image' | 'pdf' | 'audio' | 'video' | 'text' | 'markdown' | 'table' | 'json' | 'archive' | 'info'

export type ViewerItem = Pick<Item, 'name' | 'mime' | 'category' | 'ext' | 'isDir'>

/** Raster formats every browser decodes: shown directly (original at 100 %). */
export const IMAGE_DIRECT_EXT: ReadonlySet<string> = new Set([
  'jpg',
  'jpeg',
  'png',
  'gif',
  'webp',
  'avif',
  'bmp',
  'ico',
])
/** Only through the server-rendered raster preview (SVG is never inlined). */
export const IMAGE_RASTER_ONLY_EXT: ReadonlySet<string> = new Set([
  'svg',
  'tif',
  'tiff',
  'heic',
  'heif',
])
const IMAGE_MIMES: ReadonlySet<string> = new Set([
  'image/jpeg',
  'image/png',
  'image/gif',
  'image/webp',
  'image/avif',
  'image/bmp',
  'image/x-icon',
  'image/vnd.microsoft.icon',
  'image/svg+xml',
  'image/tiff',
  'image/heic',
  'image/heif',
])

/** Viewers that exist in this build (SPEC §8.14; the rest arrive in 17b). */
export const IMPLEMENTED_VIEWERS: ReadonlySet<ViewerKind> = new Set(['image', 'pdf'])

function extOf(item: ViewerItem): string {
  if (item.ext) return item.ext.toLowerCase()
  const dot = item.name.lastIndexOf('.')
  return dot > 0 ? item.name.slice(dot + 1).toLowerCase() : ''
}

export function viewerFor(item: ViewerItem): ViewerKind {
  if (item.isDir) return 'info'
  const ext = extOf(item)
  const mime = item.mime ?? ''
  if (IMAGE_DIRECT_EXT.has(ext) || IMAGE_RASTER_ONLY_EXT.has(ext) || IMAGE_MIMES.has(mime))
    return 'image'
  if (item.category === 'pdf' || mime === 'application/pdf') return 'pdf'
  if (item.category === 'archive') return 'archive'
  if (item.category === 'audio' || mime.startsWith('audio/')) return 'audio'
  if (item.category === 'video' || mime.startsWith('video/')) return 'video'
  if (ext === 'md' || ext === 'markdown' || mime === 'text/markdown') return 'markdown'
  if (ext === 'csv' || ext === 'tsv' || mime === 'text/csv' || mime === 'text/tab-separated-values')
    return 'table'
  if (ext === 'json' || mime === 'application/json') return 'json'
  if (
    item.category === 'text' ||
    item.category === 'code' ||
    mime.startsWith('text/') ||
    mime.endsWith('+xml') ||
    mime.endsWith('+json')
  )
    return 'text'
  return 'info'
}

/** How the image viewer sources a file (SPEC §8.14 ImageViewer). */
export type ImageSource = 'direct' | 'animated' | 'raster'

export function imageSourceFor(item: ViewerItem): ImageSource {
  const ext = extOf(item)
  if (ext === 'gif' || item.mime === 'image/gif') return 'animated'
  if (IMAGE_RASTER_ONLY_EXT.has(ext) || item.mime === 'image/svg+xml') return 'raster'
  return 'direct'
}

/** Prev/next stay within one viewer kind (SPEC §8.14 "file sekelas"). */
export function sameViewerKind(a: ViewerItem, b: ViewerItem): boolean {
  return viewerFor(a) === viewerFor(b)
}

/** Items the browser's "Buka" sends to the overlay in this build. */
export function opensInViewer(item: ViewerItem): boolean {
  return IMPLEMENTED_VIEWERS.has(viewerFor(item))
}
