import 'fake-indexeddb/auto'
import { beforeEach, describe, expect, it } from 'vitest'
import {
  __resetDraftStore,
  DRAFT_RETENTION_MS,
  deleteDraft,
  loadDraft,
  pruneDrafts,
  saveDraft,
} from './drafts'

describe('drafts', () => {
  beforeEach(() => __resetDraftStore())

  it('stores, loads and deletes per user and path', async () => {
    await saveDraft('u1', '/a.txt', { content: 'hello', etag: '"1-2"', at: Date.now() })
    expect((await loadDraft('u1', '/a.txt'))?.content).toBe('hello')
    expect(await loadDraft('u2', '/a.txt')).toBeNull()
    await deleteDraft('u1', '/a.txt')
    expect(await loadDraft('u1', '/a.txt')).toBeNull()
  })

  it('expires drafts after 7 days and prunes old ones', async () => {
    const old = Date.now() - DRAFT_RETENTION_MS - 1000
    await saveDraft('u1', '/old.txt', { content: 'x', etag: '"1"', at: old })
    await saveDraft('u1', '/new.txt', { content: 'y', etag: '"2"', at: Date.now() })
    expect(await loadDraft('u1', '/old.txt')).toBeNull()
    await saveDraft('u1', '/old2.txt', { content: 'z', etag: '"3"', at: old })
    expect(await pruneDrafts()).toBe(1)
    expect((await loadDraft('u1', '/new.txt'))?.content).toBe('y')
  })
})
