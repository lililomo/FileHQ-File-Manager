import { describe, expect, it, vi } from 'vitest'
import { SSEClient } from './sse'

/** Minimal EventSource double: records listeners, lets tests fire events. */
class FakeSource {
  static instances: FakeSource[] = []
  listeners = new Map<string, (ev: MessageEvent<string>) => void>()
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  constructor(public url: string) {
    FakeSource.instances.push(this)
  }
  addEventListener(name: string, fn: (ev: MessageEvent<string>) => void) {
    this.listeners.set(name, fn)
  }
  close() {
    this.closed = true
  }
  fire(name: string, data: unknown, lastEventId = '') {
    this.listeners.get(name)?.({ data: JSON.stringify(data), lastEventId } as MessageEvent<string>)
  }
}

function setup(opts: Partial<ConstructorParameters<typeof SSEClient>[0]> = {}) {
  FakeSource.instances = []
  const timers: { fn: () => void; ms: number }[] = []
  const events: [string, unknown, string | null][] = []
  const statuses: string[] = []
  const client = new SSEClient({
    url: '/api/v1/events',
    events: ['task.done', 'resync'],
    onEvent: (n, d, id) => events.push([n, d, id]),
    onStatus: (s) => statuses.push(s),
    createSource: (u) => new FakeSource(u) as unknown as EventSource,
    setTimer: (fn, ms) => {
      timers.push({ fn, ms })
      return timers.length
    },
    clearTimer: () => {},
    ...opts,
  })
  return { client, timers, events, statuses }
}

describe('SSEClient', () => {
  it('parses named events, tracks the last id and reports status', () => {
    const { client, events, statuses } = setup()
    client.connect()
    const src = FakeSource.instances[0]!
    expect(src.url).toBe('/api/v1/events')
    src.onopen?.()
    src.fire('task.done', { id: 't1' }, '10-1')
    src.fire('resync', {}, '')
    expect(events).toEqual([
      ['task.done', { id: 't1' }, '10-1'],
      ['resync', {}, null],
    ])
    expect(client.lastEventId).toBe('10-1')
    expect(statuses).toEqual(['connecting', 'open'])
    client.close()
    expect(src.closed).toBe(true)
    expect(client.status).toBe('closed')
  })

  it('reconnects with exponential backoff and passes lastEventId', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0.5) // jitter factor 1.0
    const { client, timers, statuses } = setup({ minDelayMs: 1000, maxDelayMs: 8000 })
    client.connect()
    const first = FakeSource.instances[0]!
    first.onopen?.()
    first.fire('task.done', { id: 'a' }, '5-9')
    first.onerror?.()
    expect(first.closed).toBe(true)
    expect(timers.map((t) => t.ms)).toEqual([1000])
    expect(statuses.at(-1)).toBe('reconnecting')
    timers[0]!.fn()
    const second = FakeSource.instances[1]!
    expect(second.url).toBe('/api/v1/events?lastEventId=5-9')
    second.onerror?.() // still down → 2 s, then 4 s, then capped at 8 s
    timers[1]!.fn()
    FakeSource.instances[2]!.onerror?.()
    timers[2]!.fn()
    FakeSource.instances[3]!.onerror?.()
    timers[3]!.fn()
    FakeSource.instances[4]!.onerror?.()
    expect(timers.map((t) => t.ms)).toEqual([1000, 2000, 4000, 8000, 8000])
    // a successful open resets the backoff
    timers[4]!.fn()
    const sixth = FakeSource.instances[5]!
    sixth.onopen?.()
    sixth.onerror?.()
    expect(timers.at(-1)!.ms).toBe(1000)
    client.close()
    vi.restoreAllMocks()
  })

  it('stops when shouldReconnect says no (session gone)', () => {
    let authenticated = true
    const { client, timers, statuses } = setup({ shouldReconnect: () => authenticated })
    client.connect()
    authenticated = false
    FakeSource.instances[0]!.onerror?.()
    expect(timers).toHaveLength(0)
    expect(statuses.at(-1)).toBe('closed')
    expect(SSEClient.backoff(3)).toBe(8000)
    expect(SSEClient.backoff(20)).toBe(30_000)
  })
})
