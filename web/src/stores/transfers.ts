import { create } from 'zustand'

/** Upload queue state shown by the transfer panel (SPEC §8.8 step 4).
 *  Persisted to sessionStorage so a reload still lists the unfinished
 *  transfers as paused; the `File` handles are gone then (`detached`), the
 *  user re-selects the files and tus resumes from the stored fingerprint. */
export type TransferStatus =
  'queued' | 'uploading' | 'paused' | 'error' | 'done' | 'skipped' | 'cancelled'

export type TransferResult = 'created' | 'renamed' | 'overwritten' | 'skipped'

export interface Transfer {
  id: string
  name: string
  relativePath: string
  dest: string
  size: number
  sent: number
  status: TransferStatus
  speedBps: number | null
  etaSeconds: number | null
  error?: { code: string; message: string } | undefined
  result?: TransferResult | undefined
  finalPath?: string | null | undefined
  /** The File handle was lost (page reload): needs the user to pick it again. */
  detached?: boolean | undefined
  /** Bytes already on the server when this session resumed. */
  resumedFrom?: number | undefined
  createdAt: number
  /** tus upload URL once known (debug/E2E). */
  uploadUrl?: string | null | undefined
}

export interface TransferSummary {
  active: number
  queued: number
  detached: number
  totalBytes: number
  sentBytes: number
  speedBps: number
  etaSeconds: number | null
  percent: number
}

export interface TransfersState {
  items: Record<string, Transfer>
  /** Oldest first (display order). */
  order: string[]
  panelOpen: boolean
  collapsed: boolean
  add: (t: Transfer) => void
  update: (id: string, patch: Partial<Transfer>) => void
  remove: (id: string) => void
  clearFinished: () => void
  setPanel: (open: boolean) => void
  setCollapsed: (collapsed: boolean) => void
}

export const ACTIVE_STATUSES: TransferStatus[] = ['queued', 'uploading']
export const FINAL_STATUSES: TransferStatus[] = ['done', 'skipped', 'cancelled']

export function isActiveTransfer(t: Pick<Transfer, 'status'>): boolean {
  return t.status === 'queued' || t.status === 'uploading'
}

const STORAGE_KEY = 'fm.transfers'
const MAX_PERSISTED = 500

function persistable(t: Transfer): Transfer {
  // a running/queued transfer cannot survive a reload: it needs its file again
  if (t.status === 'uploading' || t.status === 'queued' || t.status === 'paused') {
    return { ...t, status: 'paused', detached: true, speedBps: null, etaSeconds: null }
  }
  return t
}

function read(): { items: Record<string, Transfer>; order: string[] } {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return { items: {}, order: [] }
    const v = JSON.parse(raw) as { items?: Transfer[] }
    const items: Record<string, Transfer> = {}
    const order: string[] = []
    for (const t of v.items ?? []) {
      if (!t || typeof t.id !== 'string' || typeof t.name !== 'string') continue
      items[t.id] = persistable(t)
      order.push(t.id)
    }
    return { items, order }
  } catch {
    return { items: {}, order: [] }
  }
}

let writeTimer: ReturnType<typeof setTimeout> | null = null
function scheduleWrite(get: () => TransfersState): void {
  if (writeTimer) return
  writeTimer = setTimeout(() => {
    writeTimer = null
    try {
      const s = get()
      const items = s.order
        .slice(-MAX_PERSISTED)
        .map((id) => s.items[id])
        .filter((t): t is Transfer => !!t)
        .map(persistable)
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ items }))
    } catch {
      /* storage unavailable */
    }
  }, 300)
}

export const useTransfersStore = create<TransfersState>((set, get) => ({
  ...read(),
  panelOpen: false,
  collapsed: false,
  add: (t) => {
    set((s) => ({ items: { ...s.items, [t.id]: t }, order: [...s.order, t.id], panelOpen: true }))
    scheduleWrite(get)
  },
  update: (id, patch) => {
    set((s) => {
      const cur = s.items[id]
      if (!cur) return {}
      return { items: { ...s.items, [id]: { ...cur, ...patch } } }
    })
    scheduleWrite(get)
  },
  remove: (id) => {
    set((s) => {
      if (!s.items[id]) return {}
      const items = { ...s.items }
      delete items[id]
      return { items, order: s.order.filter((x) => x !== id) }
    })
    scheduleWrite(get)
  },
  clearFinished: () => {
    set((s) => {
      const items: Record<string, Transfer> = {}
      const order = s.order.filter((id) => {
        const t = s.items[id]
        if (!t) return false
        const keep = !FINAL_STATUSES.includes(t.status) && t.status !== 'error'
        if (keep) items[id] = t
        return keep
      })
      return { items, order }
    })
    scheduleWrite(get)
  },
  setPanel: (panelOpen) => set({ panelOpen }),
  setCollapsed: (collapsed) => set({ collapsed }),
}))

/** Aggregate numbers for the panel header (SPEC §8.8: total progress, speed, ETA). */
export function summarize(items: Record<string, Transfer>): TransferSummary {
  let active = 0
  let queued = 0
  let detached = 0
  let totalBytes = 0
  let sentBytes = 0
  let speedBps = 0
  for (const t of Object.values(items)) {
    if (t.detached) detached++
    if (t.status === 'uploading') {
      active++
      if (t.speedBps) speedBps += t.speedBps
    } else if (t.status === 'queued') queued++
    if (t.status === 'uploading' || t.status === 'queued' || t.status === 'paused') {
      totalBytes += t.size
      sentBytes += t.sent
    }
  }
  const remaining = totalBytes - sentBytes
  const etaSeconds = speedBps > 0 && remaining > 0 ? Math.round(remaining / speedBps) : null
  const percent = totalBytes > 0 ? Math.min(100, (sentBytes / totalBytes) * 100) : 0
  return { active, queued, detached, totalBytes, sentBytes, speedBps, etaSeconds, percent }
}

/** Test helper. */
export function resetTransfersStore(): void {
  useTransfersStore.setState({ items: {}, order: [], panelOpen: false, collapsed: false })
  try {
    sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    /* ignore */
  }
}
