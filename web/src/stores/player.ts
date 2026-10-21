import { create } from 'zustand'

/** Global music player state (SPEC §8.14 AudioPlayer, PROMPT 17b task 2).
 *  The single `HTMLAudioElement` lives in `features/viewer/audio/engine.ts`
 *  and follows this store; the store itself is plain data + pure transitions
 *  so the queue logic (repeat/shuffle/reorder) is unit-testable. */

export interface Track {
  path: string
  name: string
  mime?: string | null
  ext?: string
  size?: number
}

export type RepeatMode = 'off' | 'all' | 'one'
export const REPEAT_MODES: RepeatMode[] = ['off', 'all', 'one']
export const AUDIO_RATES = [0.5, 0.75, 1, 1.25, 1.5, 2] as const

/** Tags of the current track (GET /files/media-info). */
export interface TrackMeta {
  path: string
  title?: string
  artist?: string
  album?: string
  year?: string
  track?: string
  hasCover: boolean
  coverUrl?: string
}

export interface QueueState {
  queue: Track[]
  /** index into `queue` of the current track, −1 = nothing loaded */
  index: number
  /** play order: indices into `queue` (identity unless shuffled) */
  order: number[]
  shuffle: boolean
  repeat: RepeatMode
}

export interface PlayerState extends QueueState {
  /** desired state; the engine mirrors the element's play/pause into it */
  playing: boolean
  volume: number
  muted: boolean
  rate: number
  expanded: boolean
  currentTime: number
  duration: number
  /** `unsupported` (canPlayType) or `failed` (load/network) for the current track */
  error: 'unsupported' | 'failed' | null
  meta: TrackMeta | null
  /** bumps when the same track must restart (repeat one, prev at > 3 s) */
  restartToken: number
  playList: (tracks: Track[], start: number) => void
  add: (tracks: Track[]) => number
  remove: (queueIndex: number) => void
  move: (from: number, to: number) => void
  clear: () => void
  jump: (queueIndex: number) => void
  next: (auto?: boolean) => void
  prev: () => void
  setPlaying: (playing: boolean) => void
  toggle: () => void
  toggleShuffle: () => void
  cycleRepeat: () => void
  setRepeat: (r: RepeatMode) => void
  setVolume: (v: number) => void
  setMuted: (m: boolean) => void
  setRate: (r: number) => void
  setExpanded: (e: boolean) => void
}

let random: () => number = Math.random

/** Identity order, or a random order that starts with `current`. */
export function buildOrder(n: number, shuffle: boolean, current: number): number[] {
  const order = Array.from({ length: n }, (_, i) => i)
  if (!shuffle || n < 2) return order
  for (let i = n - 1; i > 0; i--) {
    const j = Math.floor(random() * (i + 1))
    ;[order[i], order[j]] = [order[j]!, order[i]!]
  }
  const at = order.indexOf(current)
  if (at > 0) {
    order.splice(at, 1)
    order.unshift(current)
  }
  return order
}

/** Queue index played after the current one; null = stop.
 *  `auto` = the track ended by itself (repeat one replays it). */
export function nextIndex(s: QueueState, auto: boolean): number | null {
  if (s.queue.length === 0 || s.index < 0) return null
  if (auto && s.repeat === 'one') return s.index
  const pos = s.order.indexOf(s.index)
  if (pos >= 0 && pos + 1 < s.order.length) return s.order[pos + 1]!
  if (s.repeat === 'all' || (s.repeat === 'one' && !auto)) return s.order[0]!
  return null
}

/** Queue index of "previous"; wraps only with repeat all. */
export function prevIndex(s: QueueState): number | null {
  if (s.queue.length === 0 || s.index < 0) return null
  const pos = s.order.indexOf(s.index)
  if (pos > 0) return s.order[pos - 1]!
  if (s.repeat !== 'off') return s.order[s.order.length - 1]!
  return null
}

/** Move a queue entry and keep `index` on the same track (drag reorder). */
export function moveTrack(s: QueueState, from: number, to: number): QueueState {
  if (from === to || from < 0 || to < 0 || from >= s.queue.length || to >= s.queue.length) return s
  const queue = s.queue.slice()
  const [t] = queue.splice(from, 1)
  queue.splice(to, 0, t!)
  const remap = (i: number) => {
    if (i === from) return to
    if (from < to && i > from && i <= to) return i - 1
    if (from > to && i >= to && i < from) return i + 1
    return i
  }
  return {
    ...s,
    queue,
    index: remap(s.index),
    order: s.shuffle ? s.order.map(remap) : buildOrder(queue.length, false, 0),
  }
}

/** Remove a queue entry; removing the current track selects the next one. */
export function removeTrack(s: QueueState, at: number): QueueState {
  if (at < 0 || at >= s.queue.length) return s
  const queue = s.queue.filter((_, i) => i !== at)
  let index = s.index
  if (queue.length === 0) index = -1
  else if (at < s.index) index = s.index - 1
  else if (at === s.index) index = Math.min(at, queue.length - 1)
  const order = s.order.filter((i) => i !== at).map((i) => (i > at ? i - 1 : i))
  return { ...s, queue, index, order }
}

const initialQueue: QueueState = { queue: [], index: -1, order: [], shuffle: false, repeat: 'off' }

export const usePlayerStore = create<PlayerState>((set, get) => ({
  ...initialQueue,
  playing: false,
  volume: 0.8,
  muted: false,
  rate: 1,
  expanded: false,
  currentTime: 0,
  duration: 0,
  error: null,
  meta: null,
  restartToken: 0,
  playList: (tracks, start) => {
    const index = tracks.length ? Math.max(0, Math.min(start, tracks.length - 1)) : -1
    set({
      queue: tracks,
      index,
      order: buildOrder(tracks.length, get().shuffle, index),
      playing: index >= 0,
      currentTime: 0,
      duration: 0,
      error: null,
      restartToken: get().restartToken + 1,
    })
  },
  add: (tracks) => {
    const s = get()
    const known = new Set(s.queue.map((t) => t.path))
    const fresh = tracks.filter((t) => !known.has(t.path) && known.add(t.path))
    if (!fresh.length) return 0
    const queue = [...s.queue, ...fresh]
    const added = fresh.map((_, i) => s.queue.length + i)
    let order = [...s.order, ...added]
    if (s.shuffle) {
      // new entries land at random places after the current one
      const pos = Math.max(0, order.indexOf(s.index))
      const head = order.slice(0, pos + 1)
      const tail = buildOrder(order.length - pos - 1, true, -1).map((i) => order[pos + 1 + i]!)
      order = [...head, ...tail]
    }
    set({ queue, order, index: s.index < 0 ? s.queue.length : s.index })
    return fresh.length
  },
  remove: (at) => {
    const s = get()
    const wasCurrent = at === s.index
    const nextState = removeTrack(s, at)
    set({
      ...nextState,
      ...(nextState.index < 0 ? { playing: false, currentTime: 0, duration: 0, meta: null } : {}),
      ...(wasCurrent ? { error: null, restartToken: s.restartToken + 1 } : {}),
    })
  },
  move: (from, to) => set(moveTrack(get(), from, to)),
  clear: () =>
    set({
      ...initialQueue,
      shuffle: get().shuffle,
      repeat: get().repeat,
      playing: false,
      currentTime: 0,
      duration: 0,
      meta: null,
      error: null,
    }),
  jump: (i) => {
    const s = get()
    if (i < 0 || i >= s.queue.length) return
    set({ index: i, playing: true, error: null, currentTime: 0, restartToken: s.restartToken + 1 })
  },
  next: (auto = false) => {
    const s = get()
    const n = nextIndex(s, auto)
    if (n === null) {
      // end of the list: stop on the last track, rewound
      set({ playing: false, currentTime: 0 })
      return
    }
    set({ index: n, playing: true, error: null, currentTime: 0, restartToken: s.restartToken + 1 })
  },
  prev: () => {
    const s = get()
    // like every music player: past 3 s "previous" restarts the track
    const p = s.currentTime > 3 ? s.index : prevIndex(s)
    if (p === null) {
      set({ currentTime: 0, restartToken: s.restartToken + 1 })
      return
    }
    set({ index: p, playing: true, error: null, currentTime: 0, restartToken: s.restartToken + 1 })
  },
  setPlaying: (playing) => set({ playing: playing && get().index >= 0 }),
  toggle: () => set({ playing: !get().playing && get().index >= 0 }),
  toggleShuffle: () => {
    const s = get()
    const shuffle = !s.shuffle
    set({ shuffle, order: buildOrder(s.queue.length, shuffle, s.index) })
  },
  cycleRepeat: () => set({ repeat: REPEAT_MODES[(REPEAT_MODES.indexOf(get().repeat) + 1) % 3]! }),
  setRepeat: (repeat) => set({ repeat }),
  setVolume: (v) =>
    set({ volume: Math.max(0, Math.min(1, v)), muted: v <= 0 ? get().muted : false }),
  setMuted: (muted) => set({ muted }),
  setRate: (rate) => set({ rate: Math.max(0.25, Math.min(2, rate)) }),
  setExpanded: (expanded) => set({ expanded }),
}))

/** The track being played, if any. */
export function currentTrack(s: Pick<PlayerState, 'queue' | 'index'>): Track | null {
  return s.index >= 0 ? (s.queue[s.index] ?? null) : null
}

/** Test hooks. */
export const __playerInternals = {
  setRandom(fn: () => number): void {
    random = fn
  },
  reset(): void {
    random = Math.random
    usePlayerStore.setState({
      ...initialQueue,
      playing: false,
      volume: 0.8,
      muted: false,
      rate: 1,
      expanded: false,
      currentTime: 0,
      duration: 0,
      error: null,
      meta: null,
      restartToken: 0,
    })
  },
}
