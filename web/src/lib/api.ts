/** Fetch wrapper for the JSON API (SPEC §6.1): same-origin credentials,
 *  CSRF header on non-GET (refreshed once on CSRF_INVALID), error envelope →
 *  ApiError, 401 → unauthorized handler, 403 PASSWORD_CHANGE_REQUIRED →
 *  password-change handler. */
import { useSessionStore } from '@/stores/session'
import { asUserParamFor, shareParamFor } from '@/lib/share-scope'

export const API_BASE = '/api/v1'

export interface ErrorEnvelope {
  error: { code: string; message: string; details?: Record<string, unknown> }
}

export class ApiError extends Error {
  readonly code: string
  readonly status: number
  readonly details: Record<string, unknown>
  readonly requestId: string | undefined
  readonly retryAfter: number | undefined

  constructor(
    code: string,
    status: number,
    message: string,
    details: Record<string, unknown> = {},
  ) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
    this.details = details
    this.requestId = typeof details.requestId === 'string' ? details.requestId : undefined
    this.retryAfter = typeof details.retryAfter === 'number' ? details.retryAfter : undefined
  }

  /** `details.fields` of VALIDATION_FAILED (field → reason). */
  get fields(): Record<string, string> {
    const f = this.details.fields
    if (!f || typeof f !== 'object') return {}
    const out: Record<string, string> = {}
    for (const [k, v] of Object.entries(f as Record<string, unknown>)) out[k] = String(v)
    return out
  }
}

export function isApiError(e: unknown, code?: string): e is ApiError {
  return e instanceof ApiError && (code === undefined || e.code === code)
}

export type Query = Record<string, string | number | boolean | undefined | null>

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  query?: Query
  body?: unknown
  headers?: Record<string, string>
  signal?: AbortSignal
  /** Do not call the unauthorized handler on 401 (login, session probe). */
  skipAuthRedirect?: boolean
  /** Do not call the password-change handler on 403 PASSWORD_CHANGE_REQUIRED. */
  skipPasswordChangeRedirect?: boolean
}

type Handlers = {
  onUnauthorized?: (() => void) | undefined
  onPasswordChangeRequired?: (() => void) | undefined
}
let handlers: Handlers = {}
export function setApiHandlers(h: Handlers): void {
  handlers = { ...handlers, ...h }
}

function buildUrl(path: string, query?: Query): string {
  const url = path.startsWith('/') ? API_BASE + path : `${API_BASE}/${path}`
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query ?? {})) {
    if (v === undefined || v === null) continue
    params.set(k, String(v))
  }
  // inside a received share (Step 18b) the scoped routes carry ?share=<id>
  const share = shareParamFor(path)
  if (share && !params.has('share')) params.set('share', share)
  // an admin browsing another account (Step 19a) carries ?as_user= instead
  const asUser = asUserParamFor(path)
  if (asUser && !params.has('as_user')) params.set('as_user', asUser)
  const qs = params.toString()
  return qs ? `${url}?${qs}` : url
}

async function parseEnvelope(res: Response): Promise<ApiError> {
  let body: unknown = null
  try {
    body = await res.json()
  } catch {
    /* non-JSON error */
  }
  const env = body as Partial<ErrorEnvelope> | null
  if (env && env.error && typeof env.error.code === 'string') {
    const details: Record<string, unknown> = { ...(env.error.details ?? {}) }
    const ra = res.headers.get('Retry-After')
    if (ra && details.retryAfter === undefined && /^\d+$/.test(ra)) details.retryAfter = Number(ra)
    return new ApiError(env.error.code, res.status, env.error.message, details)
  }
  return new ApiError(
    res.status >= 500 ? 'INTERNAL' : 'UNKNOWN',
    res.status,
    res.statusText || 'request failed',
  )
}

let csrfRefresh: Promise<string> | null = null
async function refreshCsrf(): Promise<string> {
  if (!csrfRefresh) {
    csrfRefresh = (async () => {
      const res = await fetch(`${API_BASE}/auth/csrf`, {
        credentials: 'same-origin',
        headers: { Accept: 'application/json' },
      })
      if (!res.ok) throw await parseEnvelope(res)
      const data = (await res.json()) as { csrfToken: string }
      useSessionStore.getState().setCsrfToken(data.csrfToken)
      return data.csrfToken
    })().finally(() => {
      csrfRefresh = null
    })
  }
  return csrfRefresh
}

export async function api<T = void>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? 'GET'
  const unsafe = method !== 'GET'
  const doFetch = async (csrf: string | null): Promise<Response> => {
    const headers: Record<string, string> = { Accept: 'application/json', ...(opts.headers ?? {}) }
    const form = typeof FormData !== 'undefined' && opts.body instanceof FormData
    if (opts.body !== undefined && !form) headers['Content-Type'] = 'application/json'
    if (unsafe && csrf) headers['X-CSRF-Token'] = csrf
    const init: RequestInit = { method, credentials: 'same-origin', headers }
    if (form) init.body = opts.body as FormData
    else if (opts.body !== undefined) init.body = JSON.stringify(opts.body)
    if (opts.signal) init.signal = opts.signal
    try {
      return await fetch(buildUrl(path, opts.query), init)
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') throw e
      throw new ApiError('NETWORK', 0, e instanceof Error ? e.message : 'network error')
    }
  }

  let res = await doFetch(useSessionStore.getState().csrfToken)
  if (res.status === 403 && unsafe) {
    const err = await parseEnvelope(res.clone())
    if (err.code === 'CSRF_INVALID') {
      const fresh = await refreshCsrf()
      res = await doFetch(fresh)
    }
  }
  if (res.ok) {
    if (res.status === 204 || res.headers.get('Content-Length') === '0') return undefined as T
    return (await res.json()) as T
  }
  const err = await parseEnvelope(res)
  if (err.status === 401 && !opts.skipAuthRedirect) {
    useSessionStore.getState().setAnonymous()
    handlers.onUnauthorized?.()
  }
  if (err.code === 'PASSWORD_CHANGE_REQUIRED' && !opts.skipPasswordChangeRedirect) {
    handlers.onPasswordChangeRequired?.()
  }
  throw err
}
