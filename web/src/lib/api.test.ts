import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useSessionStore } from '@/stores/session'
import { ApiError, api, setApiHandlers } from './api'

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })

describe('api()', () => {
  beforeEach(() => {
    useSessionStore.setState({
      status: 'authenticated',
      user: null,
      config: null,
      csrfToken: 'tok1',
    })
    setApiHandlers({ onUnauthorized: undefined, onPasswordChangeRequired: undefined })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('parses the error envelope into ApiError', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        json(404, {
          error: { code: 'NOT_FOUND', message: 'nope', details: { requestId: 'r-1' } },
        }),
      ),
    )
    const err = await api('/files/stat', { query: { path: '/x' } }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ code: 'NOT_FOUND', status: 404, requestId: 'r-1', message: 'nope' })
  })

  it('sends the CSRF header, refreshes it once on CSRF_INVALID and retries', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json(403, { error: { code: 'CSRF_INVALID', message: 'bad token' } }))
      .mockResolvedValueOnce(json(200, { csrfToken: 'tok2' }))
      .mockResolvedValueOnce(json(201, { item: { name: 'a' } }))
    vi.stubGlobal('fetch', fetchMock)
    const res = await api<{ item: { name: string } }>('/files/mkdir', {
      method: 'POST',
      body: { path: '/a' },
    })
    expect(res.item.name).toBe('a')
    expect(fetchMock).toHaveBeenCalledTimes(3)
    const first = fetchMock.mock.calls[0]![1] as RequestInit
    expect((first.headers as Record<string, string>)['X-CSRF-Token']).toBe('tok1')
    expect(fetchMock.mock.calls[1]![0]).toBe('/api/v1/auth/csrf')
    const retry = fetchMock.mock.calls[2]![1] as RequestInit
    expect((retry.headers as Record<string, string>)['X-CSRF-Token']).toBe('tok2')
    expect(retry.credentials).toBe('same-origin')
    expect(useSessionStore.getState().csrfToken).toBe('tok2')
  })

  it('clears the session and calls the handler on 401', async () => {
    const onUnauthorized = vi.fn()
    setApiHandlers({ onUnauthorized })
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })),
    )
    await expect(api('/auth/me')).rejects.toMatchObject({ code: 'UNAUTHENTICATED' })
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(useSessionStore.getState().status).toBe('anonymous')
  })

  it('does not redirect on 401 when skipAuthRedirect is set', async () => {
    const onUnauthorized = vi.fn()
    setApiHandlers({ onUnauthorized })
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(json(401, { error: { code: 'UNAUTHENTICATED', message: 'x' } })),
    )
    await expect(
      api('/auth/login', { method: 'POST', body: {}, skipAuthRedirect: true }),
    ).rejects.toBeInstanceOf(ApiError)
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('calls the password-change handler on PASSWORD_CHANGE_REQUIRED', async () => {
    const onPasswordChangeRequired = vi.fn()
    setApiHandlers({ onPasswordChangeRequired })
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          json(403, { error: { code: 'PASSWORD_CHANGE_REQUIRED', message: 'x' } }),
        ),
    )
    await expect(api('/me/preferences')).rejects.toMatchObject({ code: 'PASSWORD_CHANGE_REQUIRED' })
    expect(onPasswordChangeRequired).toHaveBeenCalledTimes(1)
  })

  it('resolves to undefined on 204 and exposes Retry-After', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    await expect(api('/auth/logout', { method: 'POST' })).resolves.toBeUndefined()
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          json(
            429,
            { error: { code: 'RATE_LIMITED', message: 'slow down' } },
            { 'Retry-After': '42' },
          ),
        ),
    )
    const err = (await api('/auth/login', {
      method: 'POST',
      body: {},
      skipAuthRedirect: true,
    }).catch((e: unknown) => e)) as ApiError
    expect(err.retryAfter).toBe(42)
  })

  it('maps network failures to NETWORK', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    await expect(api('/auth/me')).rejects.toMatchObject({ code: 'NETWORK', status: 0 })
  })
})
