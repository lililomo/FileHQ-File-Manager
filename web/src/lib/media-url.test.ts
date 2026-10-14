import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useSessionStore } from '@/stores/session'
import { __mediaUrlInternals, fetchSigned, getMediaUrl, invalidateMediaUrl } from './media-url'

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

describe('getMediaUrl', () => {
  let t0 = 1_800_000_000_000
  beforeEach(() => {
    __mediaUrlInternals.reset()
    __mediaUrlInternals.setNow(() => t0)
    useSessionStore.setState({
      status: 'authenticated',
      user: null,
      config: null,
      csrfToken: 'tok',
    })
  })
  afterEach(() => vi.unstubAllGlobals())

  const signer = (ttlMs: number) => {
    let n = 0
    return vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/files/sign')) {
        n++
        return Promise.resolve(
          json(200, {
            url: `/api/v1/files/raw?path=%2Fa.mp4&sig=s${n}`,
            expiresAt: new Date(t0 + ttlMs).toISOString(),
            purpose: 'media',
          }),
        )
      }
      return Promise.resolve(new Response('bytes', { status: 200 }))
    })
  }

  it('caches a URL until 60 s before it expires and de-duplicates in-flight signing', async () => {
    const fetchMock = signer(2 * 60 * 60 * 1000)
    vi.stubGlobal('fetch', fetchMock)
    const [u1, u2] = await Promise.all([getMediaUrl('/a.mp4'), getMediaUrl('/a.mp4')])
    expect(u1).toBe(u2)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(await getMediaUrl('/a.mp4')).toBe(u1)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    // one minute before expiry a new signature is requested
    t0 += 2 * 60 * 60 * 1000 - 59_000
    expect(await getMediaUrl('/a.mp4')).not.toBe(u1)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('separates purposes, honours force and invalidation', async () => {
    const fetchMock = signer(10 * 60 * 1000)
    vi.stubGlobal('fetch', fetchMock)
    await getMediaUrl('/a.mp4', 'media')
    await getMediaUrl('/a.mp4', 'inline')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await getMediaUrl('/a.mp4', 'media', { force: true })
    expect(fetchMock).toHaveBeenCalledTimes(3)
    invalidateMediaUrl('/a.mp4')
    expect(__mediaUrlInternals.cacheSize()).toBe(0)
    await getMediaUrl('/a.mp4')
    expect(fetchMock).toHaveBeenCalledTimes(4)
  })

  it('fetchSigned re-signs once on 403 SIGNATURE_EXPIRED', async () => {
    let contentCalls = 0
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/files/sign')) {
        return Promise.resolve(
          json(200, {
            url: `/api/v1/files/raw?path=%2Fa.pdf&sig=${contentCalls}`,
            expiresAt: new Date(t0 + 60_000 * 10).toISOString(),
            purpose: 'inline',
          }),
        )
      }
      contentCalls++
      if (contentCalls === 1)
        return Promise.resolve(
          json(403, { error: { code: 'SIGNATURE_EXPIRED', message: 'signed url expired' } }),
        )
      return Promise.resolve(new Response('%PDF', { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const res = await fetchSigned('/a.pdf', 'inline')
    expect(res.status).toBe(200)
    expect(contentCalls).toBe(2)
    // sign, content(403), sign again, content(200)
    expect(fetchMock).toHaveBeenCalledTimes(4)
    // a plain 403 is not retried
    contentCalls = 5
    const res2 = await fetchSigned('/b.pdf', 'inline')
    expect(res2.status).toBe(200)
  })
})
