/** Local drafts of unsaved tabs (SPEC §8.13 "Draft lokal"): IndexedDB via
 *  idb-keyval, key `draft:<userId>:<path>`, retention 7 days. */
import { createStore, del, get, keys, set, type UseStore } from 'idb-keyval'

export interface Draft {
  content: string
  etag: string
  at: number
}

export const DRAFT_RETENTION_MS = 7 * 24 * 60 * 60 * 1000

let store: UseStore | null = null
function db(): UseStore {
  store ??= createStore('fm-editor', 'drafts')
  return store
}

export function draftKey(userId: string, path: string): string {
  return `draft:${userId}:${path}`
}

export async function saveDraft(userId: string, path: string, draft: Draft): Promise<void> {
  try {
    await set(draftKey(userId, path), draft, db())
  } catch {
    /* IndexedDB unavailable (private mode): drafts are best effort */
  }
}

export async function loadDraft(userId: string, path: string): Promise<Draft | null> {
  try {
    const d = await get<Draft>(draftKey(userId, path), db())
    if (!d || typeof d.content !== 'string') return null
    if (Date.now() - d.at > DRAFT_RETENTION_MS) {
      await del(draftKey(userId, path), db())
      return null
    }
    return d
  } catch {
    return null
  }
}

export async function deleteDraft(userId: string, path: string): Promise<void> {
  try {
    await del(draftKey(userId, path), db())
  } catch {
    /* ignore */
  }
}

/** Drops every draft older than the retention window. Returns how many were removed. */
export async function pruneDrafts(now = Date.now()): Promise<number> {
  try {
    const all = await keys<string>(db())
    let n = 0
    for (const k of all) {
      if (typeof k !== 'string' || !k.startsWith('draft:')) continue
      const d = await get<Draft>(k, db())
      if (!d || now - d.at > DRAFT_RETENTION_MS) {
        await del(k, db())
        n++
      }
    }
    return n
  } catch {
    return 0
  }
}

/** Test hook: reset the cached store (fake-indexeddb). */
export function __resetDraftStore(): void {
  store = null
}
