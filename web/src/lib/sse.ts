/** Server-Sent Events client (SPEC §6.4): one `EventSource` per tab, manual
 *  reconnect with exponential backoff (1 s → 30 s) and the last seen event id
 *  passed as `?lastEventId=` so the broker replays what was missed (the
 *  browser only resends `Last-Event-ID` on its own automatic retries). */

export type SSEHandler = (name: string, data: unknown, id: string | null) => void

export interface SSEClientOptions {
  url: string
  /** Event names to listen for (unnamed `message` events are ignored). */
  events: string[]
  onEvent: SSEHandler
  onStatus?: (status: SSEStatus) => void
  /** Stop reconnecting (e.g. after a 401). Called before each reconnect. */
  shouldReconnect?: () => boolean
  minDelayMs?: number
  maxDelayMs?: number
  /** Injectable for tests. */
  createSource?: (url: string) => EventSource
  setTimer?: (fn: () => void, ms: number) => number
  clearTimer?: (id: number) => void
}

export type SSEStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'

export class SSEClient {
  private source: EventSource | null = null
  private timer: number | null = null
  private attempt = 0
  private closed = false
  lastEventId: string | null = null
  status: SSEStatus = 'closed'

  constructor(private readonly opts: SSEClientOptions) {}

  connect(): void {
    if (this.closed) return
    this.clearPending()
    const url = this.lastEventId
      ? `${this.opts.url}${this.opts.url.includes('?') ? '&' : '?'}lastEventId=${encodeURIComponent(this.lastEventId)}`
      : this.opts.url
    this.setStatus(this.attempt === 0 ? 'connecting' : 'reconnecting')
    const create = this.opts.createSource ?? ((u: string) => new EventSource(u))
    let src: EventSource
    try {
      src = create(url)
    } catch {
      this.scheduleReconnect()
      return
    }
    this.source = src
    src.onopen = () => {
      this.attempt = 0
      this.setStatus('open')
    }
    src.onerror = () => {
      // close and reconnect ourselves so the backoff and lastEventId apply
      this.dropSource()
      this.scheduleReconnect()
    }
    for (const name of this.opts.events) {
      src.addEventListener(name, (ev) => {
        const me = ev as MessageEvent<string>
        if (me.lastEventId) this.lastEventId = me.lastEventId
        let data: unknown
        try {
          data = me.data ? JSON.parse(me.data) : null
        } catch {
          data = me.data
        }
        this.opts.onEvent(name, data, me.lastEventId || null)
      })
    }
  }

  close(): void {
    this.closed = true
    this.clearPending()
    this.dropSource()
    this.setStatus('closed')
  }

  private dropSource(): void {
    if (this.source) {
      this.source.onopen = null
      this.source.onerror = null
      this.source.close()
      this.source = null
    }
  }

  private clearPending(): void {
    if (this.timer !== null) {
      ;(this.opts.clearTimer ?? ((id: number) => window.clearTimeout(id)))(this.timer)
      this.timer = null
    }
  }

  private scheduleReconnect(): void {
    if (this.closed || this.timer !== null) return
    if (this.opts.shouldReconnect && !this.opts.shouldReconnect()) {
      this.close()
      return
    }
    const min = this.opts.minDelayMs ?? 1000
    const max = this.opts.maxDelayMs ?? 30_000
    const delay = Math.min(max, min * 2 ** this.attempt) * (0.75 + Math.random() * 0.5)
    this.attempt += 1
    this.setStatus('reconnecting')
    const set = this.opts.setTimer ?? ((fn: () => void, ms: number) => window.setTimeout(fn, ms))
    this.timer = set(() => {
      this.timer = null
      this.connect()
    }, delay)
  }

  private setStatus(s: SSEStatus): void {
    if (this.status === s) return
    this.status = s
    this.opts.onStatus?.(s)
  }

  /** Delay before the next attempt (ms) — exported for tests. */
  static backoff(attempt: number, min = 1000, max = 30_000): number {
    return Math.min(max, min * 2 ** attempt)
  }
}
