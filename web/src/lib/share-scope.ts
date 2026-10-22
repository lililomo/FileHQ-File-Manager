/**
 * Received-share scope (Step 18b, SPEC §8.15 "Dibagikan ke saya"). While the
 * recipient browses `/shared-with-me/<id>/*` (or edits a shared file at
 * `/editor?share=<id>`), every session endpoint that accepts `?share=` is
 * called inside the share: `api()` appends the parameter for the scoped
 * route prefixes, uploads go through `/upload/share/<id>/tus/`, and signed
 * URLs come back bound to the recipient. Paths are relative to the shared
 * item ("/" = the shared folder, or the shared file itself).
 */
// kept local: api.ts imports this module (no import cycle)
const API_BASE = '/api/v1'

export type SharePermission = 'viewer' | 'editor'

export interface ShareScope {
  shareId: string
  permission: SharePermission
  /** the shared item is a single file: only path "/" exists */
  isFile: boolean
  name: string
  allowDownload: boolean
  /** Step 19a: an admin browsing this account's root (`?as_user=`), no share involved */
  asUser?: string
}

let scope: ShareScope | null = null
const listeners = new Set<() => void>()

export function setShareScope(next: ShareScope | null): void {
  scope = next
  for (const fn of listeners) fn()
}

export function getShareScope(): ShareScope | null {
  return scope
}

export function isShareScope(): boolean {
  return scope !== null
}

export function canEditShare(): boolean {
  return scope?.permission === 'editor'
}

export function onShareScopeChange(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** API route prefixes (without `/api/v1`) that run inside the share when a
 *  scope is active. Everything else (`/me/*`, `/tasks`, `/notifications`,
 *  `/shares`, `/trash`…) stays the recipient's own. */
const SCOPED_PREFIXES = [
  '/files/list',
  '/files/stat',
  '/files/content',
  '/files/sign',
  '/files/zip',
  '/files/media-info',
  '/files/mkdir',
  '/files/create',
  '/files/rename',
  '/files/copy',
  '/files/move',
  '/files/duplicate',
  '/files/delete',
  '/versions',
  '/upload/precheck',
]

function scopedPath(path: string): boolean {
  const p = path.startsWith('/') ? path : `/${path}`
  for (const prefix of SCOPED_PREFIXES) {
    if (p === prefix || p.startsWith(`${prefix}?`) || p.startsWith(`${prefix}/`)) return true
  }
  return false
}

/** The share id to append to `path`, or null when the call is not scoped. */
export function shareParamFor(path: string): string | null {
  if (!scope || scope.asUser) return null
  return scopedPath(path) ? scope.shareId : null
}

/** The username to append as `as_user` (admin browse), or null. */
export function asUserParamFor(path: string): string | null {
  if (!scope?.asUser) return null
  return scopedPath(path) ? scope.asUser : null
}

/** True while an admin browses another account. */
export function isAsUserScope(): boolean {
  return !!scope?.asUser
}

/** tus base of the active share (`/api/v1/upload/share/<id>`). */
export function shareUploadBase(shareId = scope?.shareId ?? ''): string {
  return `${API_BASE}/upload/share/${encodeURIComponent(shareId)}`
}

/** Route for `api()` of the precheck inside the active share. */
export function shareUploadPrecheckPath(): string {
  return `upload/share/${encodeURIComponent(scope?.shareId ?? '')}/precheck`
}
