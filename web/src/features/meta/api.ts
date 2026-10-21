/** Item metadata API (SPEC §6.3 "Meta", §8.7; Step 10b): favorites, pins,
 *  notes and colour tags. Writes accept `paths[]` (batch) or `path`. */
import type { Item } from '@/features/browser/api'
import { api } from '@/lib/api'
import { isPublicScope } from '@/lib/public-scope'

export const TAG_COLORS = ['red', 'orange', 'yellow', 'green', 'blue', 'purple', 'gray'] as const
export type TagColor = (typeof TAG_COLORS)[number]
export function isTagColor(v: unknown): v is TagColor {
  return typeof v === 'string' && (TAG_COLORS as readonly string[]).includes(v)
}

export const MAX_NOTE_CHARS = 500
export const MAX_PINS_PER_FOLDER = 50

export interface MetaResult {
  paths: string[]
  count: number
}

export interface ItemsResponse {
  items: Item[]
  total: number
}

export const FAVORITES_QUERY_KEY = ['favorites'] as const

export function listFavorites(signal?: AbortSignal): Promise<ItemsResponse> {
  return api<ItemsResponse>('/meta/favorites', signal ? { signal } : {})
}

export function addFavorites(paths: string[]): Promise<MetaResult> {
  return api<MetaResult>('/meta/favorites', { method: 'PUT', body: { paths } })
}

export function removeFavorites(paths: string[]): Promise<void> {
  return api<void>('/meta/favorites', { method: 'DELETE', body: { paths } })
}

export function addPins(paths: string[]): Promise<MetaResult> {
  return api<MetaResult>('/meta/pins', { method: 'PUT', body: { paths } })
}

export function removePins(paths: string[]): Promise<void> {
  return api<void>('/meta/pins', { method: 'DELETE', body: { paths } })
}

export function setNote(path: string, note: string): Promise<{ path: string; note: string }> {
  return api<{ path: string; note: string }>('/meta/notes', { method: 'PUT', body: { path, note } })
}

export function removeNote(path: string): Promise<void> {
  return api<void>('/meta/notes', { method: 'DELETE', body: { path } })
}

export function setTags(paths: string[], color: TagColor): Promise<MetaResult> {
  return api<MetaResult>('/meta/tags', { method: 'PUT', body: { paths, color } })
}

export function removeTags(paths: string[]): Promise<void> {
  return api<void>('/meta/tags', { method: 'DELETE', body: { paths } })
}

export function listTagged(color: TagColor, signal?: AbortSignal): Promise<ItemsResponse> {
  return api<ItemsResponse>(
    '/meta/tagged',
    signal ? { query: { color }, signal } : { query: { color } },
  )
}

/** Records an "open" so Recent reflects reads (viewer, Quick Look). */
export function recordOpen(path: string): Promise<void> {
  if (isPublicScope()) return Promise.resolve()
  return api<void>('/me/recent', { method: 'POST', body: { path } })
}
