/**
 * Public share scope (Step 18a). While a guest is on `/s/<token>` the viewer,
 * thumbnail, download and upload helpers talk to `/api/v1/public/shares/<token>/*`
 * instead of the session endpoints: no signed URLs (the token/slug plus the
 * unlock cookie are the authorisation), no `/me/*` side effects. The page sets
 * the scope on mount and clears it on unmount.
 */
import { API_BASE } from '@/lib/api'

export interface PublicScope {
  token: string
}

let scope: PublicScope | null = null
const listeners = new Set<() => void>()

export function setPublicScope(next: PublicScope | null): void {
  scope = next
  for (const fn of listeners) fn()
}

export function getPublicScope(): PublicScope | null {
  return scope
}

export function isPublicScope(): boolean {
  return scope !== null
}

export function onPublicScopeChange(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** `/api/v1/public/shares/<token>` for the active scope (or a given token). */
export function publicBase(token = scope?.token ?? ''): string {
  return `${API_BASE}/public/shares/${encodeURIComponent(token)}`
}

/** Content URL inside the active public share (`raw`, `download`, `thumb`…). */
export function publicUrl(
  route: string,
  query: Record<string, string | number | undefined>,
): string {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== '') params.set(k, String(v))
  }
  const qs = params.toString()
  return `${publicBase()}/${route}${qs ? `?${qs}` : ''}`
}

/** Route for `api()` (no leading slash: `api()` prefixes `/api/v1` itself). */
export function publicApiPath(route: string): string {
  return `public/shares/${encodeURIComponent(scope?.token ?? '')}/${route}`
}
