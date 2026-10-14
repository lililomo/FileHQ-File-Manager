import { create } from 'zustand'

/** Application clipboard (SPEC §8.5): copy/cut paths across folder tabs and
 *  browser tabs. Persisted in sessionStorage and mirrored through a
 *  BroadcastChannel so another browser tab sees the same clipboard. */
export type ClipboardMode = 'copy' | 'cut'

export interface ClipboardData {
  mode: ClipboardMode
  paths: string[]
  /** folder the items were copied/cut from */
  from: string
  /** folder tab id (null when set from another browser tab) */
  sourceTab: string | null
}

export interface ClipboardState {
  data: ClipboardData | null
  set: (data: ClipboardData) => void
  clear: () => void
}

const STORAGE_KEY = 'fm.clipboard'
const CHANNEL = 'fm-clipboard'

function read(): ClipboardData | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const v = JSON.parse(raw) as Partial<ClipboardData>
    if ((v.mode !== 'copy' && v.mode !== 'cut') || !Array.isArray(v.paths)) return null
    return {
      mode: v.mode,
      paths: v.paths.filter((p): p is string => typeof p === 'string'),
      from: typeof v.from === 'string' ? v.from : '/',
      sourceTab: null,
    }
  } catch {
    return null
  }
}

function write(data: ClipboardData | null): void {
  try {
    if (data) sessionStorage.setItem(STORAGE_KEY, JSON.stringify(data))
    else sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    /* storage unavailable */
  }
}

let channel: BroadcastChannel | null = null
function getChannel(): BroadcastChannel | null {
  if (channel) return channel
  if (typeof BroadcastChannel === 'undefined') return null
  channel = new BroadcastChannel(CHANNEL)
  channel.onmessage = (ev: MessageEvent<ClipboardData | null>) => {
    const d = ev.data
    useClipboardStore.setState({ data: d ? { ...d, sourceTab: null } : null })
    write(d)
  }
  return channel
}

export const useClipboardStore = create<ClipboardState>((set) => ({
  data: typeof sessionStorage !== 'undefined' ? read() : null,
  set: (data) => {
    set({ data })
    write(data)
    getChannel()?.postMessage(data)
  },
  clear: () => {
    set({ data: null })
    write(null)
    getChannel()?.postMessage(null)
  },
}))

/** Paths currently cut (rendered faded). */
export function cutPaths(data: ClipboardData | null): ReadonlySet<string> {
  return data && data.mode === 'cut' ? new Set(data.paths) : EMPTY
}
const EMPTY: ReadonlySet<string> = new Set()

/** Test helper: reset module state. */
export function resetClipboard(): void {
  useClipboardStore.setState({ data: null })
  write(null)
}
