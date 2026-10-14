import { beforeEach, describe, expect, it } from 'vitest'
import { resetTransfersStore, summarize, useTransfersStore, type Transfer } from './transfers'

function tr(id: string, patch: Partial<Transfer> = {}): Transfer {
  return {
    id,
    name: `${id}.bin`,
    relativePath: '',
    dest: '/',
    size: 100,
    sent: 0,
    status: 'queued',
    speedBps: null,
    etaSeconds: null,
    createdAt: 1,
    ...patch,
  }
}

describe('transfers store', () => {
  beforeEach(() => resetTransfersStore())

  it('adds in order, opens the panel, updates and clears finished rows', () => {
    const s = useTransfersStore.getState()
    s.add(tr('a'))
    s.add(tr('b', { status: 'done', sent: 100 }))
    s.add(tr('c', { status: 'error' }))
    expect(useTransfersStore.getState().order).toEqual(['a', 'b', 'c'])
    expect(useTransfersStore.getState().panelOpen).toBe(true)
    s.update('a', { status: 'uploading', sent: 40, speedBps: 10 })
    expect(useTransfersStore.getState().items.a?.sent).toBe(40)
    s.clearFinished()
    expect(useTransfersStore.getState().order).toEqual(['a'])
  })

  it('summarises bytes, speed, ETA and detached rows', () => {
    const s = useTransfersStore.getState()
    s.add(tr('a', { status: 'uploading', sent: 50, speedBps: 10 }))
    s.add(tr('b', { status: 'queued' }))
    s.add(tr('c', { status: 'paused', detached: true, sent: 20 }))
    s.add(tr('d', { status: 'done', sent: 100 }))
    const sum = summarize(useTransfersStore.getState().items)
    expect(sum.active).toBe(1)
    expect(sum.queued).toBe(1)
    expect(sum.detached).toBe(1)
    expect(sum.totalBytes).toBe(300)
    expect(sum.sentBytes).toBe(70)
    expect(sum.speedBps).toBe(10)
    expect(sum.etaSeconds).toBe(23)
    expect(Math.round(sum.percent)).toBe(23)
  })

  it('persists unfinished rows as detached/paused for the next page load', async () => {
    const s = useTransfersStore.getState()
    s.add(tr('a', { status: 'uploading', sent: 50 }))
    s.add(tr('b', { status: 'done', sent: 100 }))
    await new Promise((r) => setTimeout(r, 400))
    const raw = JSON.parse(sessionStorage.getItem('fm.transfers') ?? '{}') as { items: Transfer[] }
    expect(raw.items.map((t) => [t.id, t.status, t.detached ?? false])).toEqual([
      ['a', 'paused', true],
      ['b', 'done', false],
    ])
  })
})
