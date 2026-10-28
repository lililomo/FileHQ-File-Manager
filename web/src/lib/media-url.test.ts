import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useSessionStore } from '@/stores/session'
import { setPublicScope } from '@/lib/public-scope'
import {
  __mediaUrlInternals,
  contentCredentials,
  fetchSigned,
  getMediaUrl,
  invalidateMediaUrl,
  isCrossOrigin,
  signDownloadUrl,
} from './media-url'

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

// Step 23e: absolute URLs from a separate content origin, cookie-less fetches
describe('content origin (Step 23e)', () => {
  beforeEach(() => {
    __mediaUrlInternals.reset()
    setPublicScope(null)
    useSessionStore.setState({
      status: 'authenticated',
      user: null,
      config: null,
      csrfToken: 'tok',
    })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    setPublicScope(null)
  })

  it('isCrossOrigin / contentCredentials: never cookies to another origin', () => {
    expect(isCrossOrigin('/api/v1/files/raw?x')).toBe(false)
    expect(isCrossOrigin(`${window.location.origin}/api/v1/files/raw`)).toBe(false)
    expect(isCrossOrigin('https://usercontent.example.test/api/v1/files/raw?x')).toBe(true)
    expect(contentCredentials('https://usercontent.example.test/api/v1/files/raw')).toBe('omit')
    expect(contentCredentials('/api/v1/files/raw')).toBe('omit')
    setPublicScope({ token: 'tok' })
    expect(contentCredentials('/api/v1/public/shares/tok/raw')).toBe('same-origin')
    expect(
      contentCredentials('https://usercontent.example.test/api/v1/public/shares/tok/raw'),
    ).toBe('omit')
  })

  it('uses the absolute URL returned by /files/sign as is and fetches it without credentials', async () => {
    const abs =
      'https://usercontent.example.test/api/v1/files/raw?path=%2Fa.pdf&purpose=inline&sig=s1'
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.includes('/files/sign'))
        return Promise.resolve(
          json(200, {
            url: abs,
            expiresAt: new Date(Date.now() + 600_000).toISOString(),
            purpose: 'inline',
          }),
        )
      expect(url).toBe(abs)
      expect(init?.credentials).toBe('omit')
      return Promise.resolve(new Response('%PDF', { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    expect(await getMediaUrl('/a.pdf', 'inline')).toBe(abs)
    const res = await fetchSigned('/a.pdf', 'inline')
    expect(res.status).toBe(200)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('guests on a content origin sign through POST …/sign; same-origin guests keep the cookie URL', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/public/shares/tok/sign')) {
        const body = JSON.parse(String(init?.body)) as { path: string; purpose: string }
        return Promise.resolve(
          json(200, {
            url: `https://usercontent.example.test/api/v1/public/shares/tok/raw?path=${encodeURIComponent(body.path)}&purpose=${body.purpose}&scope=s%3A1&sig=x`,
            expiresAt: new Date(Date.now() + 600_000).toISOString(),
            purpose: body.purpose,
          }),
        )
      }
      return Promise.resolve(new Response('bytes', { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    setPublicScope({ token: 'tok', contentBaseUrl: 'https://usercontent.example.test' })
    const u = await getMediaUrl('/foto.jpg', 'media')
    expect(u).toBe(
      'https://usercontent.example.test/api/v1/public/shares/tok/raw?path=%2Ffoto.jpg&purpose=media&scope=s%3A1&sig=x',
    )
    expect(await signDownloadUrl('/foto.jpg')).toContain(
      '/public/shares/tok/raw?path=%2Ffoto.jpg&purpose=download',
    )
    const res = await fetchSigned('/foto.jpg', 'media')
    expect(res.status).toBe(200)
    const last = fetchMock.mock.calls.at(-1)!
    expect(String(last[0])).toContain('usercontent.example.test')
    expect(last[1]?.credentials).toBe('omit')
    // same origin: no signing call, cookie-authorised URL
    fetchMock.mockClear()
    __mediaUrlInternals.reset()
    setPublicScope({ token: 'tok' })
    expect(await getMediaUrl('/foto.jpg', 'media')).toBe(
      '/api/v1/public/shares/tok/raw?path=%2Ffoto.jpg&purpose=media',
    )
    expect(await signDownloadUrl('/foto.jpg')).toBe(
      '/api/v1/public/shares/tok/download?path=%2Ffoto.jpg',
    )
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('cache isolation between the user scope and a guest scope (Step 23e)', () => {
  beforeEach(() => {
    __mediaUrlInternals.reset()
    setPublicScope(null)
    useSessionStore.setState({
      status: 'authenticated',
      user: null,
      config: null,
      csrfToken: 'tok',
    })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    setPublicScope(null)
  })
  it('the same path string inside a share does not reuse the user-signed URL', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      const exp = new Date(Date.now() + 600_000).toISOString()
      if (url.endsWith('/files/sign'))
        return Promise.resolve(
          json(200, {
            url: '/api/v1/files/raw?path=%2Fa.jpg&sig=user',
            expiresAt: exp,
            purpose: 'media',
          }),
        )
      if (url.endsWith('/public/shares/tok/sign'))
        return Promise.resolve(
          json(200, {
            url: 'https://usercontent.example.test/api/v1/public/shares/tok/raw?path=%2Fa.jpg&sig=guest',
            expiresAt: exp,
            purpose: 'media',
          }),
        )
      return Promise.resolve(new Response('', { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    expect(await getMediaUrl('/a.jpg')).toContain('sig=user')
    setPublicScope({ token: 'tok', contentBaseUrl: 'https://usercontent.example.test' })
    expect(await getMediaUrl('/a.jpg')).toContain('sig=guest')
    setPublicScope(null)
    expect(await getMediaUrl('/a.jpg')).toContain('sig=user')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
