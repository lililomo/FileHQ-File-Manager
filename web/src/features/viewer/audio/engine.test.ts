import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { __playerInternals, usePlayerStore } from '@/stores/player'
import { useSessionStore, type User } from '@/stores/session'

vi.mock('@/lib/media-url', () => ({
  getMediaUrl: vi.fn(
    async (path: string) => `/api/v1/files/raw?path=${encodeURIComponent(path)}&sig=test`,
  ),
  isSignatureExpired: vi.fn(async () => false),
}))
vi.mock('@/features/viewer/api', () => ({
  getMediaInfo: vi.fn(async () => ({ tags: {}, hasCover: false })),
}))

import { audioElement, startAudioEngine } from './engine'

const user = (id: string): User => ({
  id,
  username: id,
  displayName: id,
  role: 'user',
  status: 'active',
  mustChangePassword: false,
  homeProvisioned: true,
  quotaBytes: 0,
  createdAt: '2026-10-21T00:00:00Z',
})
const track = { path: '/musik/satu.mp3', name: 'satu.mp3' }

describe('audio engine follows the session', () => {
  let stop = () => {}
  beforeEach(() => {
    __playerInternals.reset()
    // jsdom has no media playback: stub the element so the engine can load
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably')
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
    useSessionStore.getState().setAuthenticated(user('a'), null)
    stop = startAudioEngine()
  })
  afterEach(() => {
    stop()
    usePlayerStore.getState().clear()
    useSessionStore.getState().setAnonymous()
    vi.restoreAllMocks()
  })

  it('logout clears the queue and unloads the element', async () => {
    usePlayerStore.getState().playList([track], 0)
    expect(usePlayerStore.getState().index).toBe(0)
    await vi.waitFor(() => expect(audioElement()?.getAttribute('src')).toContain('satu.mp3'))
    useSessionStore.getState().setAnonymous()
    const s = usePlayerStore.getState()
    expect(s.index).toBe(-1)
    expect(s.queue).toEqual([])
    expect(s.playing).toBe(false)
    expect(audioElement()?.getAttribute('src')).toBeNull()
  })

  it('another user signing in on the same tab starts from an empty player', () => {
    usePlayerStore.getState().playList([track], 0)
    useSessionStore.getState().setAuthenticated(user('b'), null)
    expect(usePlayerStore.getState().queue).toEqual([])
  })

  it('refreshing the same user keeps the queue', () => {
    usePlayerStore.getState().playList([track], 0)
    useSessionStore.getState().setAuthenticated(user('a'), null)
    useSessionStore.getState().updateUser({ displayName: 'Pengguna A' })
    expect(usePlayerStore.getState().index).toBe(0)
  })
})
