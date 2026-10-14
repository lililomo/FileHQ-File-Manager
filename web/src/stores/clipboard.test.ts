import { beforeEach, describe, expect, it } from 'vitest'
import { cutPaths, resetClipboard, useClipboardStore } from './clipboard'

describe('clipboard store', () => {
  beforeEach(() => resetClipboard())

  it('stores copy/cut payloads and persists them in sessionStorage', () => {
    useClipboardStore
      .getState()
      .set({ mode: 'copy', paths: ['/a.txt'], from: '/', sourceTab: 'tab-1' })
    expect(useClipboardStore.getState().data).toEqual({
      mode: 'copy',
      paths: ['/a.txt'],
      from: '/',
      sourceTab: 'tab-1',
    })
    const raw = JSON.parse(sessionStorage.getItem('fm.clipboard') ?? 'null') as { mode: string }
    expect(raw.mode).toBe('copy')
    useClipboardStore.getState().clear()
    expect(useClipboardStore.getState().data).toBeNull()
    expect(sessionStorage.getItem('fm.clipboard')).toBeNull()
  })

  it('exposes cut paths for the faded rendering, but not copied ones', () => {
    useClipboardStore
      .getState()
      .set({ mode: 'cut', paths: ['/x', '/y'], from: '/', sourceTab: null })
    expect([...cutPaths(useClipboardStore.getState().data)]).toEqual(['/x', '/y'])
    useClipboardStore.getState().set({ mode: 'copy', paths: ['/x'], from: '/', sourceTab: null })
    expect(cutPaths(useClipboardStore.getState().data).size).toBe(0)
    expect(cutPaths(null).size).toBe(0)
  })

  it('ignores malformed persisted data', () => {
    sessionStorage.setItem('fm.clipboard', '{"mode":"bogus"}')
    // a fresh read happens at module init; emulate it through resetClipboard + manual read
    resetClipboard()
    expect(useClipboardStore.getState().data).toBeNull()
  })
})
