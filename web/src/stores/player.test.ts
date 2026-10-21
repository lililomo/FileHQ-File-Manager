import { beforeEach, describe, expect, it } from 'vitest'
import {
  __playerInternals,
  buildOrder,
  currentTrack,
  moveTrack,
  nextIndex,
  prevIndex,
  removeTrack,
  usePlayerStore,
  type QueueState,
  type Track,
} from './player'

const tracks = (n: number): Track[] =>
  Array.from({ length: n }, (_, i) => ({ path: `/m/${i}.mp3`, name: `${i}.mp3` }))
const q = (over: Partial<QueueState> = {}): QueueState => ({
  queue: tracks(4),
  index: 0,
  order: [0, 1, 2, 3],
  shuffle: false,
  repeat: 'off',
  ...over,
})

beforeEach(() => __playerInternals.reset())

describe('repeat', () => {
  it('off: stops after the last track, manual next too', () => {
    expect(nextIndex(q({ index: 2 }), true)).toBe(3)
    expect(nextIndex(q({ index: 3 }), true)).toBeNull()
    expect(nextIndex(q({ index: 3 }), false)).toBeNull()
    expect(prevIndex(q({ index: 0 }))).toBeNull()
  })
  it('all: wraps both ways', () => {
    expect(nextIndex(q({ index: 3, repeat: 'all' }), true)).toBe(0)
    expect(prevIndex(q({ index: 0, repeat: 'all' }))).toBe(3)
  })
  it('one: the end of a track replays it, a manual next still moves on', () => {
    expect(nextIndex(q({ index: 1, repeat: 'one' }), true)).toBe(1)
    expect(nextIndex(q({ index: 1, repeat: 'one' }), false)).toBe(2)
    expect(nextIndex(q({ index: 3, repeat: 'one' }), false)).toBe(0)
  })
  it('store: next at the end with repeat off pauses; repeat one bumps the restart token', () => {
    const s = usePlayerStore.getState()
    s.playList(tracks(2), 1)
    expect(usePlayerStore.getState().playing).toBe(true)
    usePlayerStore.getState().next(true)
    expect(usePlayerStore.getState()).toMatchObject({ index: 1, playing: false })
    usePlayerStore.getState().setRepeat('one')
    usePlayerStore.getState().setPlaying(true)
    const token = usePlayerStore.getState().restartToken
    usePlayerStore.getState().next(true)
    expect(usePlayerStore.getState().index).toBe(1)
    expect(usePlayerStore.getState().restartToken).toBe(token + 1)
    usePlayerStore.getState().cycleRepeat() // one → off
    expect(usePlayerStore.getState().repeat).toBe('off')
    usePlayerStore.getState().cycleRepeat()
    expect(usePlayerStore.getState().repeat).toBe('all')
  })
  it('prev past 3 s restarts the current track', () => {
    usePlayerStore.getState().playList(tracks(3), 2)
    usePlayerStore.setState({ currentTime: 12 })
    usePlayerStore.getState().prev()
    expect(usePlayerStore.getState().index).toBe(2)
    usePlayerStore.getState().prev()
    expect(usePlayerStore.getState().index).toBe(1)
  })
})

describe('shuffle', () => {
  it('is a permutation that starts with the current track and visits every track once', () => {
    let seed = 7
    __playerInternals.setRandom(() => {
      seed = (seed * 9301 + 49297) % 233280
      return seed / 233280
    })
    const order = buildOrder(10, true, 4)
    expect(order[0]).toBe(4)
    expect([...order].sort((a, b) => a - b)).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 8, 9])
    expect(order).not.toEqual([4, 0, 1, 2, 3, 5, 6, 7, 8, 9])
    // walking the order with repeat off ends after n tracks
    const s = q({ queue: tracks(10), index: 4, order, shuffle: true })
    const seen = [s.index]
    let cur: number | null = s.index
    while ((cur = nextIndex({ ...s, index: cur }, true)) !== null) seen.push(cur)
    expect(seen).toHaveLength(10)
    expect(new Set(seen).size).toBe(10)
  })
  it('toggling keeps the current track and restores queue order when off', () => {
    usePlayerStore.getState().playList(tracks(5), 3)
    usePlayerStore.getState().toggleShuffle()
    const on = usePlayerStore.getState()
    expect(on.shuffle).toBe(true)
    expect(on.order[0]).toBe(3)
    expect(currentTrack(on)!.path).toBe('/m/3.mp3')
    usePlayerStore.getState().toggleShuffle()
    expect(usePlayerStore.getState().order).toEqual([0, 1, 2, 3, 4])
  })
})

describe('queue edits', () => {
  it('move keeps the current track selected (drag reorder)', () => {
    const s = moveTrack(q({ index: 1 }), 1, 3)
    expect(s.queue.map((t) => t.name)).toEqual(['0.mp3', '2.mp3', '3.mp3', '1.mp3'])
    expect(s.index).toBe(3)
    const t = moveTrack(q({ index: 2 }), 0, 3)
    expect(t.queue[t.index]!.name).toBe('2.mp3')
  })
  it('remove before / at / after the current track', () => {
    expect(removeTrack(q({ index: 2 }), 0).index).toBe(1)
    const at = removeTrack(q({ index: 2 }), 2)
    expect(at.queue[at.index]!.name).toBe('3.mp3')
    expect(removeTrack(q({ index: 3 }), 3).index).toBe(2)
    expect(removeTrack(q({ index: 0, queue: tracks(1), order: [0] }), 0).index).toBe(-1)
  })
  it('add appends without duplicates; clear empties', () => {
    usePlayerStore.getState().playList(tracks(2), 0)
    expect(usePlayerStore.getState().add([...tracks(3), { path: '/x.mp3', name: 'x.mp3' }])).toBe(2)
    expect(usePlayerStore.getState().queue.map((t) => t.name)).toEqual([
      '0.mp3',
      '1.mp3',
      '2.mp3',
      'x.mp3',
    ])
    expect(usePlayerStore.getState().order).toEqual([0, 1, 2, 3])
    usePlayerStore.getState().clear()
    expect(usePlayerStore.getState()).toMatchObject({ queue: [], index: -1, playing: false })
    // adding to an empty player selects the first added track without playing it
    usePlayerStore.getState().add(tracks(2))
    expect(usePlayerStore.getState()).toMatchObject({ index: 0, playing: false })
  })
})
