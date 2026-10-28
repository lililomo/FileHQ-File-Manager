/**
 * Signed content URLs (SPEC §8.9, ADR-15). `getMediaUrl` signs a path for
 * `<img>/<video>/<audio>`/fetch use and caches the URL until 60 s before it
 * expires; a `403 SIGNATURE_EXPIRED` from the content endpoint refreshes it.
 * Step 17 viewers call `getMediaUrl`, download actions `signDownloadUrl`.
 */
import { api } from '@/lib/api'
import { getPublicScope, isPublicScope, publicContentOrigin, publicUrl } from '@/lib/public-scope'
import { signPublicUrl } from '@/features/public/publicApi'

export type SignPurpose = 'download' | 'media' | 'inline'

export interface SignedUrl {
  url: string
  expiresAt: string
  purpose: SignPurpose
}

/** Refresh this long before `expiresAt` so a playing media element never hits the edge. */
export const REFRESH_MARGIN_MS = 60_000

interface Entry {
  url: string
  /** ms since epoch after which the cached URL is no longer handed out */
  until: number
}

const cache = new Map<string, Entry>()
const inflight = new Map<string, Promise<string>>()
let now = () => Date.now()

// Step 23e: guest URLs on a content origin are cached too — keyed by the
// share token so a path inside a share never collides with the user's own
const key = (path: string, purpose: SignPurpose) =>
  `${getPublicScope()?.token ?? ''}\u0000${purpose}\u0000${path}`

export function signUrl(path: string, purpose: SignPurpose): Promise<SignedUrl> {
  // guests on a separate content origin (Step 23e): the share scope signs
  if (isPublicScope() && publicContentOrigin()) {
    return signPublicUrl(getPublicScope()?.token ?? '', path, purpose) as Promise<SignedUrl>
  }
  return api<SignedUrl>('/files/sign', { method: 'POST', body: { path, purpose } })
}

/** True for `http(s)://…` URLs on another origin (the content origin). */
export function isCrossOrigin(url: string): boolean {
  if (!/^https?:\/\//i.test(url)) return false
  if (typeof window === 'undefined') return true
  try {
    return new URL(url).origin !== window.location.origin
  } catch {
    return false
  }
}

/** Credentials mode for a content request: never send cookies to the
 *  content origin; guests on the same origin still need the unlock cookie. */
export function contentCredentials(url: string): RequestCredentials {
  return isPublicScope() && !isCrossOrigin(url) ? 'same-origin' : 'omit'
}

/** Signed URL for a viewer; cached per (path, purpose) until exp − 60 s. */
export async function getMediaUrl(
  path: string,
  purpose: Exclude<SignPurpose, 'download'> = 'media',
  opts: { force?: boolean } = {},
): Promise<string> {
  // guests on the same origin: the token/cookie authorise, nothing to sign
  if (isPublicScope() && !publicContentOrigin()) return publicUrl('raw', { path, purpose })
  const k = key(path, purpose)
  if (!opts.force) {
    const hit = cache.get(k)
    if (hit && hit.until > now()) return hit.url
    const pending = inflight.get(k)
    if (pending) return pending
  }
  const p = signUrl(path, purpose)
    .then((res) => {
      const exp = Date.parse(res.expiresAt)
      const until = Number.isFinite(exp) ? exp - REFRESH_MARGIN_MS : now()
      cache.set(k, { url: res.url, until })
      return res.url
    })
    .finally(() => inflight.delete(k))
  inflight.set(k, p)
  return p
}

/** Fresh, uncached download URL (TTL 5 min, one use case: the click that follows). */
export async function signDownloadUrl(path: string): Promise<string> {
  if (isPublicScope() && !publicContentOrigin()) return publicUrl('download', { path })
  return (await signUrl(path, 'download')).url
}

/** Forget cached URLs for a path (after rename/move/delete), or everything. */
export function invalidateMediaUrl(path?: string): void {
  if (path === undefined) {
    cache.clear()
    return
  }
  for (const purpose of ['media', 'inline'] as const) cache.delete(key(path, purpose))
}

/** True when a content response says the signed URL has expired. */
export async function isSignatureExpired(res: Response): Promise<boolean> {
  if (res.status !== 403) return false
  try {
    const body = (await res.clone().json()) as { error?: { code?: string } }
    return body.error?.code === 'SIGNATURE_EXPIRED'
  } catch {
    return false
  }
}

/**
 * Fetch a signed resource; on `403 SIGNATURE_EXPIRED` the URL is re-signed
 * once and the request retried (pdf.js / text viewers / thumbnails).
 */
export async function fetchSigned(
  path: string,
  purpose: Exclude<SignPurpose, 'download'> = 'media',
  init?: RequestInit,
): Promise<Response> {
  let url = await getMediaUrl(path, purpose)
  let res = await fetch(url, { credentials: contentCredentials(url), ...init })
  if (await isSignatureExpired(res)) {
    url = await getMediaUrl(path, purpose, { force: true })
    res = await fetch(url, { credentials: contentCredentials(url), ...init })
  }
  return res
}

/** Start a browser download of a signed URL via an anchor (absolute URLs on
 *  the content origin work too: the response is always an attachment). */
export function startDownload(url: string, filename?: string): void {
  const a = document.createElement('a')
  a.href = url
  a.download = filename ?? ''
  a.rel = 'noopener'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  // leave the element long enough for the navigation to be scheduled
  setTimeout(() => a.remove(), 1000)
}

/** Test hooks. */
export const __mediaUrlInternals = {
  reset(): void {
    cache.clear()
    inflight.clear()
    now = () => Date.now()
  },
  setNow(fn: () => number): void {
    now = fn
  },
  cacheSize(): number {
    return cache.size
  },
}
