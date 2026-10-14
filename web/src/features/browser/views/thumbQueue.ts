/** Client side of the thumbnail pipeline (SPEC §8.4):
 *  - signed URLs are requested in batches (`POST /files/sign`, one call per
 *    ~50 ms of requests, ≤ 500 paths) and cached until shortly before expiry;
 *  - at most `MAX_PARALLEL` thumbnails load at once; waiting requests are
 *    served newest-first (what scrolled into view last) and are dropped when
 *    the cell leaves the viewport before its turn;
 *  - a 202 (still rendering) retries with backoff 2/4/8 s, at most
 *    `MAX_RETRIES` times; a 404 (unsupported/failed) shows the type icon at once.
 *
 *  Pure logic lives here so it is unit-tested without a DOM. */
import { signThumbs, type ThumbSize } from '../api'

export const MAX_PARALLEL = 4
export const BATCH_DELAY_MS = 50
export const MAX_BATCH = 500
export const MAX_RETRIES = 3
export const RETRY_DELAYS_MS = [2000, 4000, 8000]
/** Re-sign this long before the server-side expiry. */
export const EXPIRY_MARGIN_MS = 60_000

export type ThumbStatus = 'idle' | 'queued' | 'loading' | 'ready' | 'failed'

export type Signer = (
  paths: string[],
  size: ThumbSize,
) => Promise<{ urls: { path: string; url: string }[]; expiresAt: string }>

interface Waiter {
  etag: string
  resolve: (url: string | null) => void
}

/** Cancellation token shared by a cell's request chain. */
export class CancelToken {
  cancelled = false
  private cbs: (() => void)[] = []
  cancel(): void {
    if (this.cancelled) return
    this.cancelled = true
    for (const cb of this.cbs) cb()
    this.cbs = []
  }
  onCancel(cb: () => void): void {
    if (this.cancelled) cb()
    else this.cbs.push(cb)
  }
}

/** Batches signing requests per size and caches the URLs. The cache key
 *  includes the item etag so a changed file is signed/loaded afresh. */
export class UrlCache {
  private cache = new Map<string, { url: string; expiresAt: number }>()
  private pending = new Map<ThumbSize, Map<string, Waiter[]>>()
  private timers = new Map<ThumbSize, ReturnType<typeof setTimeout>>()
  signCalls = 0

  constructor(
    private readonly sign: Signer = signThumbs,
    private readonly now: () => number = () => Date.now(),
    private readonly delay = BATCH_DELAY_MS,
  ) {}

  private key(path: string, etag: string, size: ThumbSize) {
    return `${size}|${etag}|${path}`
  }

  /** Cached URL when still valid, else null. */
  peek(path: string, etag: string, size: ThumbSize): string | null {
    const k = this.key(path, etag, size)
    const c = this.cache.get(k)
    if (!c) return null
    if (c.expiresAt - EXPIRY_MARGIN_MS <= this.now()) {
      this.cache.delete(k)
      return null
    }
    return c.url
  }

  /** Resolves with the signed URL (null when the server rejected the path or
   *  the signing call failed). */
  get(path: string, etag: string, size: ThumbSize): Promise<string | null> {
    const hit = this.peek(path, etag, size)
    if (hit) return Promise.resolve(hit)
    return new Promise((resolve) => {
      let bySize = this.pending.get(size)
      if (!bySize) {
        bySize = new Map()
        this.pending.set(size, bySize)
      }
      const waiters = bySize.get(path) ?? []
      waiters.push({ etag, resolve })
      bySize.set(path, waiters)
      if (bySize.size >= MAX_BATCH) {
        this.flush(size)
        return
      }
      if (!this.timers.has(size)) {
        this.timers.set(
          size,
          setTimeout(() => this.flush(size), this.delay),
        )
      }
    })
  }

  /** Sends the pending batch for a size now. */
  flush(size: ThumbSize): void {
    const t = this.timers.get(size)
    if (t) clearTimeout(t)
    this.timers.delete(size)
    const bySize = this.pending.get(size)
    if (!bySize || bySize.size === 0) return
    this.pending.delete(size)
    const paths = [...bySize.keys()]
    this.signCalls++
    this.sign(paths, size).then(
      (res) => {
        const expiresAt = Date.parse(res.expiresAt) || this.now() + 5 * 60_000
        const got = new Map(res.urls.map((u) => [u.path, u.url]))
        for (const path of paths) {
          const url = got.get(path) ?? null
          for (const w of bySize.get(path) ?? []) {
            if (url) this.cache.set(this.key(path, w.etag, size), { url, expiresAt })
            w.resolve(url)
          }
        }
      },
      () => {
        for (const path of paths) for (const w of bySize.get(path) ?? []) w.resolve(null)
      },
    )
  }

  clear(): void {
    this.cache.clear()
  }
}

/** Load slots: at most `max` images in flight, newest waiter first. */
export class SlotQueue {
  private active = 0
  private waiting: { token: CancelToken; start: () => void }[] = []

  constructor(private readonly max = MAX_PARALLEL) {}

  get inFlight(): number {
    return this.active
  }
  get queued(): number {
    return this.waiting.length
  }

  /** Resolves with a release function, or null when the token is cancelled
   *  before a slot is granted. */
  acquire(token: CancelToken): Promise<(() => void) | null> {
    return new Promise((resolve) => {
      const entry = {
        token,
        start: () => {
          this.active++
          let released = false
          resolve(() => {
            if (released) return
            released = true
            this.active--
            this.pump()
          })
        },
      }
      token.onCancel(() => {
        const i = this.waiting.indexOf(entry)
        if (i >= 0) {
          this.waiting.splice(i, 1)
          resolve(null)
        }
      })
      if (token.cancelled) return
      this.waiting.push(entry)
      this.pump()
    })
  }

  private pump() {
    while (this.active < this.max && this.waiting.length > 0) {
      this.waiting.pop()!.start() // LIFO: the most recently visible cell first
    }
  }
}

/** Backoff for attempt n (0-based); undefined once retries are exhausted. */
export function retryDelay(attempt: number): number | undefined {
  if (attempt >= MAX_RETRIES) return undefined
  return RETRY_DELAYS_MS[Math.min(attempt, RETRY_DELAYS_MS.length - 1)]
}

export type LoadResult = 'ok' | 'pending' | 'failed'

/** Loads one thumbnail URL with fetch so the status is known: 200 → the body
 *  is read fully (which populates the browser HTTP cache the <img> then hits),
 *  202 → still rendering (retry with backoff), anything else → failed (the
 *  cell shows the type icon, no retries). */
export async function loadThumb(url: string): Promise<LoadResult> {
  try {
    const res = await fetch(url, { credentials: 'same-origin', cache: 'default' })
    if (res.status === 202) return 'pending'
    if (!res.ok) return 'failed'
    await res.arrayBuffer()
    return 'ok'
  } catch {
    return 'failed'
  }
}

export const urlCache = new UrlCache()
export const slots = new SlotQueue()

export interface ThumbRequest {
  path: string
  etag: string
  size: ThumbSize
  token: CancelToken
  onStatus: (status: ThumbStatus, src: string | null) => void
}

export interface ThumbDeps {
  cache?: UrlCache
  slots?: SlotQueue
  load?: (url: string) => Promise<LoadResult>
  sleep?: (ms: number, token: CancelToken) => Promise<void>
}

function defaultSleep(ms: number, token: CancelToken): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms)
    token.onCancel(() => {
      clearTimeout(t)
      resolve()
    })
  })
}

/** Full client flow for one cell: sign → slot → load → retry with backoff. */
export async function requestThumb(req: ThumbRequest, deps: ThumbDeps = {}): Promise<void> {
  const cache = deps.cache ?? urlCache
  const queue = deps.slots ?? slots
  const load = deps.load ?? loadThumb
  const sleep = deps.sleep ?? defaultSleep
  req.onStatus('queued', null)
  const url = await cache.get(req.path, req.etag, req.size)
  if (req.token.cancelled) return
  if (!url) {
    req.onStatus('failed', null)
    return
  }
  for (let attempt = 0; ; attempt++) {
    const release = await queue.acquire(req.token)
    if (!release) return
    req.onStatus('loading', null)
    const result = await load(url).finally(release)
    if (req.token.cancelled) return
    if (result === 'ok') {
      req.onStatus('ready', url)
      return
    }
    const delay = result === 'pending' ? retryDelay(attempt) : undefined
    if (delay === undefined) {
      req.onStatus('failed', null)
      return
    }
    req.onStatus('queued', null)
    await sleep(delay, req.token)
    if (req.token.cancelled) return
  }
}
