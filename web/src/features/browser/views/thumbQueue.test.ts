import { describe, expect, it, vi } from 'vitest'
import {
  CancelToken,
  MAX_PARALLEL,
  SlotQueue,
  UrlCache,
  requestThumb,
  retryDelay,
  type Signer,
} from './thumbQueue'

const future = () => new Date(Date.now() + 2 * 3600_000).toISOString()

function signer(calls: string[][]): Signer {
  return async (paths) => {
    calls.push(paths)
    return {
      urls: paths.filter((p) => p !== '/bad').map((p) => ({ path: p, url: `/t?p=${p}` })),
      expiresAt: future(),
    }
  }
}

describe('UrlCache', () => {
  it('batches concurrent requests into one sign call and caches by etag', async () => {
    const calls: string[][] = []
    const c = new UrlCache(signer(calls), () => Date.now(), 5)
    const [a, b, bad] = await Promise.all([
      c.get('/a.png', 'e1', 256),
      c.get('/b.png', 'e1', 256),
      c.get('/bad', 'e1', 256),
    ])
    expect(calls).toHaveLength(1)
    expect(calls[0]).toEqual(['/a.png', '/b.png', '/bad'])
    expect(a).toBe('/t?p=/a.png')
    expect(b).toBe('/t?p=/b.png')
    expect(bad).toBeNull()
    // cache hit: no second call
    expect(await c.get('/a.png', 'e1', 256)).toBe('/t?p=/a.png')
    expect(calls).toHaveLength(1)
    // changed file (new etag) → re-sign
    await c.get('/a.png', 'e2', 256)
    expect(calls).toHaveLength(2)
    // different size is a separate batch
    await c.get('/a.png', 'e2', 128)
    expect(calls).toHaveLength(3)
  })

  it('expires cached urls one minute before the server expiry', async () => {
    let now = 1_000_000
    const sign: Signer = async (paths) => ({
      urls: paths.map((p) => ({ path: p, url: 'u' })),
      expiresAt: new Date(now + 90_000).toISOString(),
    })
    const c = new UrlCache(sign, () => now, 1)
    await c.get('/x', 'e', 128)
    expect(c.peek('/x', 'e', 128)).toBe('u')
    now += 31_000 // 59 s before expiry < 60 s margin
    expect(c.peek('/x', 'e', 128)).toBeNull()
  })

  it('resolves null for everyone when signing fails', async () => {
    const c = new UrlCache(
      async () => Promise.reject(new Error('boom')),
      () => Date.now(),
      1,
    )
    expect(await c.get('/x', 'e', 128)).toBeNull()
  })
})

describe('SlotQueue', () => {
  it('never exceeds MAX_PARALLEL and serves the newest waiter first', async () => {
    const q = new SlotQueue()
    const releases: (() => void)[] = []
    for (let i = 0; i < MAX_PARALLEL; i++) releases.push((await q.acquire(new CancelToken()))!)
    expect(q.inFlight).toBe(MAX_PARALLEL)
    const order: string[] = []
    const p1 = q.acquire(new CancelToken()).then((r) => (order.push('first'), r!()))
    const p2 = q.acquire(new CancelToken()).then((r) => (order.push('second'), r!()))
    expect(q.queued).toBe(2)
    releases[0]!()
    await Promise.resolve()
    expect(order).toEqual(['second']) // LIFO
    releases[1]!()
    await Promise.all([p1, p2])
    expect(order).toEqual(['second', 'first'])
  })

  it('drops a waiter that is cancelled before its turn', async () => {
    const q = new SlotQueue(1)
    const hold = (await q.acquire(new CancelToken()))!
    const token = new CancelToken()
    const waiting = q.acquire(token)
    expect(q.queued).toBe(1)
    token.cancel()
    expect(await waiting).toBeNull()
    expect(q.queued).toBe(0)
    hold()
  })
})

describe('requestThumb', () => {
  it('retries a 202 with 2/4/8 s backoff then fails', async () => {
    const statuses: string[] = []
    const sleeps: number[] = []
    const load = vi.fn(async () => 'pending' as const)
    await requestThumb(
      {
        path: '/a',
        etag: 'e',
        size: 256,
        token: new CancelToken(),
        onStatus: (s) => statuses.push(s),
      },
      {
        cache: new UrlCache(signer([]), () => Date.now(), 1),
        slots: new SlotQueue(),
        load,
        sleep: async (ms) => {
          sleeps.push(ms)
        },
      },
    )
    expect(load).toHaveBeenCalledTimes(4)
    expect(sleeps).toEqual([2000, 4000, 8000])
    expect(statuses.at(-1)).toBe('failed')
    expect(retryDelay(3)).toBeUndefined()
  })

  it('fails at once on 404 (unsupported) without retrying', async () => {
    const load = vi.fn(async () => 'failed' as const)
    const statuses: string[] = []
    await requestThumb(
      {
        path: '/a',
        etag: 'e',
        size: 256,
        token: new CancelToken(),
        onStatus: (s) => statuses.push(s),
      },
      { cache: new UrlCache(signer([]), () => Date.now(), 1), slots: new SlotQueue(), load },
    )
    expect(load).toHaveBeenCalledTimes(1)
    expect(statuses.at(-1)).toBe('failed')
  })

  it('reports ready with the signed url on success and stops when cancelled', async () => {
    const statuses: [string, string | null][] = []
    const token = new CancelToken()
    await requestThumb(
      { path: '/a', etag: 'e', size: 128, token, onStatus: (s, src) => statuses.push([s, src]) },
      {
        cache: new UrlCache(signer([]), () => Date.now(), 1),
        slots: new SlotQueue(),
        load: async () => 'ok' as const,
      },
    )
    expect(statuses).toEqual([
      ['queued', null],
      ['loading', null],
      ['ready', '/t?p=/a'],
    ])
    const after: string[] = []
    const t2 = new CancelToken()
    const p = requestThumb(
      { path: '/b', etag: 'e', size: 128, token: t2, onStatus: (s) => after.push(s) },
      {
        cache: new UrlCache(signer([]), () => Date.now(), 20),
        slots: new SlotQueue(),
        load: async () => 'ok' as const,
      },
    )
    t2.cancel()
    await p
    expect(after).toEqual(['queued'])
  })
})
