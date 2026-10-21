/** The one global `HTMLAudioElement` (SPEC §8.14 AudioPlayer): it lives
 *  outside React so playback survives route changes, dialogs and the viewer
 *  closing. It follows `stores/player.ts` (track, play/pause, volume, rate)
 *  and reports back time, duration, end of track and errors. Media Session
 *  metadata and actions are kept in sync for the OS media controls. */
import { getMediaInfo } from '@/features/viewer/api'
import { getMediaUrl, isSignatureExpired } from '@/lib/media-url'
import { usePrefsStore } from '@/stores/prefs'
import { useSessionStore } from '@/stores/session'
import { currentTrack, usePlayerStore, type RepeatMode, type Track } from '@/stores/player'

/** MIME used for `canPlayType` by extension (the listing MIME may be generic). */
const AUDIO_TYPES: Record<string, string> = {
  mp3: 'audio/mpeg',
  m4a: 'audio/mp4; codecs="mp4a.40.2"',
  aac: 'audio/aac',
  mp4: 'audio/mp4',
  ogg: 'audio/ogg',
  oga: 'audio/ogg',
  opus: 'audio/ogg; codecs="opus"',
  wav: 'audio/wav',
  flac: 'audio/flac',
  weba: 'audio/webm',
  webm: 'audio/webm',
}

export function audioTypeOf(track: Pick<Track, 'name' | 'ext' | 'mime'>): string {
  const ext = (track.ext || track.name.split('.').pop() || '').toLowerCase()
  return AUDIO_TYPES[ext] ?? track.mime ?? ''
}

/** True when the browser says it cannot decode the track at all. */
export function cannotPlay(el: Pick<HTMLMediaElement, 'canPlayType'>, track: Track): boolean {
  const type = audioTypeOf(track)
  return !!type && el.canPlayType(type) === ''
}

let audio: HTMLAudioElement | null = null
let loadedPath: string | null = null
let loadedToken = -1
let refreshing = false
/** true between choosing a track and assigning its signed URL */
let loading = false
/** pause events caused by the engine itself (track change), not the user */
let selfPauses = 0
let lastTimeReport = 0

function el(): HTMLAudioElement {
  if (!audio) {
    audio = new Audio()
    audio.preload = 'metadata'
    audio.setAttribute('data-testid', 'global-audio')
    wire(audio)
  }
  return audio
}

/** The engine's element (E2E and the full player read `currentTime` from it). */
export function audioElement(): HTMLAudioElement | null {
  return audio
}

export function seekAudio(seconds: number): void {
  const a = audio
  if (!a || !Number.isFinite(seconds)) return
  a.currentTime = Math.max(0, Math.min(seconds, Number.isFinite(a.duration) ? a.duration : seconds))
  usePlayerStore.setState({ currentTime: a.currentTime })
}

function wire(a: HTMLAudioElement): void {
  a.addEventListener('timeupdate', () => {
    const now = performance.now()
    if (now - lastTimeReport < 250) return
    lastTimeReport = now
    usePlayerStore.setState({ currentTime: a.currentTime })
    updatePositionState(a)
  })
  a.addEventListener('durationchange', () => {
    usePlayerStore.setState({ duration: Number.isFinite(a.duration) ? a.duration : 0 })
  })
  a.addEventListener('ended', () => usePlayerStore.getState().next(true))
  a.addEventListener('play', () => {
    if (!usePlayerStore.getState().playing) usePlayerStore.setState({ playing: true })
    setPlaybackState('playing')
  })
  a.addEventListener('pause', () => {
    if (selfPauses > 0) {
      selfPauses--
      return
    }
    // pauses we did not ask for (OS controls, headset, end of list)
    if (usePlayerStore.getState().playing && !a.ended) usePlayerStore.setState({ playing: false })
    setPlaybackState('paused')
  })
  a.addEventListener('error', () => void onError(a))
}

/** Expired signature (TTL 2 h) → re-sign and resume where it stopped;
 *  anything else → the player shows "cannot play" + download. */
async function onError(a: HTMLAudioElement): Promise<void> {
  const track = currentTrack(usePlayerStore.getState())
  if (!track || refreshing) return
  const at = a.currentTime
  try {
    const res = await fetch(a.currentSrc || a.src, {
      headers: { Range: 'bytes=0-0' },
      credentials: 'omit',
    })
    if (await isSignatureExpired(res)) {
      refreshing = true
      const url = await getMediaUrl(track.path, 'media', { force: true })
      a.src = url
      a.currentTime = at
      if (usePlayerStore.getState().playing) await a.play().catch(() => {})
      return
    }
  } catch {
    /* network: fall through */
  } finally {
    refreshing = false
  }
  usePlayerStore.setState({ error: 'failed', playing: false })
}

async function load(track: Track, token: number): Promise<void> {
  const a = el()
  loadedPath = track.path
  loadedToken = token
  usePlayerStore.setState({
    meta: { path: track.path, hasCover: false },
    currentTime: 0,
    duration: 0,
  })
  setMetadata(track, null)
  void loadMeta(track)
  if (!a.paused) {
    selfPauses++
    a.pause()
  }
  if (cannotPlay(a, track)) {
    a.removeAttribute('src')
    a.load()
    usePlayerStore.setState({ error: 'unsupported', playing: false })
    return
  }
  loading = true
  try {
    const url = await getMediaUrl(track.path, 'media')
    if (loadedPath !== track.path || loadedToken !== token) return // superseded
    a.src = url
    a.playbackRate = usePlayerStore.getState().rate
    loading = false
    if (usePlayerStore.getState().playing) await a.play().catch(() => {})
  } catch {
    usePlayerStore.setState({ error: 'failed', playing: false })
  } finally {
    if (loadedPath === track.path) loading = false
  }
}

async function loadMeta(track: Track): Promise<void> {
  try {
    const info = await getMediaInfo(track.path)
    const tags = info.tags ?? {}
    let coverUrl: string | undefined
    if (info.hasCover) {
      const signed = await getMediaUrl(track.path, 'media')
      coverUrl = `${signed}&variant=thumb`
    }
    if (loadedPath !== track.path) return
    const meta = {
      path: track.path,
      hasCover: info.hasCover,
      ...(tags.title ? { title: tags.title } : {}),
      ...(tags.artist ? { artist: tags.artist } : {}),
      ...(tags.album ? { album: tags.album } : {}),
      ...(tags.year ? { year: tags.year } : {}),
      ...(tags.track ? { track: tags.track } : {}),
      ...(coverUrl ? { coverUrl } : {}),
    }
    usePlayerStore.setState({ meta })
    setMetadata(track, meta)
  } catch {
    /* tags are optional */
  }
}

// --- Media Session API ---

function session(): MediaSession | null {
  return typeof navigator !== 'undefined' && 'mediaSession' in navigator
    ? navigator.mediaSession
    : null
}

function setMetadata(
  track: Track,
  meta: { title?: string; artist?: string; album?: string; coverUrl?: string } | null,
): void {
  const ms = session()
  if (!ms || typeof MediaMetadata === 'undefined') return
  ms.metadata = new MediaMetadata({
    title: meta?.title || track.name,
    artist: meta?.artist ?? '',
    album: meta?.album ?? '',
    artwork: meta?.coverUrl ? [{ src: meta.coverUrl, sizes: '512x512', type: 'image/webp' }] : [],
  })
}

function setPlaybackState(state: MediaSessionPlaybackState): void {
  const ms = session()
  if (ms) ms.playbackState = state
}

function updatePositionState(a: HTMLAudioElement): void {
  const ms = session()
  if (!ms?.setPositionState || !Number.isFinite(a.duration) || a.duration <= 0) return
  try {
    ms.setPositionState({
      duration: a.duration,
      position: Math.min(a.currentTime, a.duration),
      playbackRate: a.playbackRate,
    })
  } catch {
    /* some browsers reject transient values */
  }
}

function installSessionHandlers(): void {
  const ms = session()
  if (!ms) return
  const st = () => usePlayerStore.getState()
  const handlers: [MediaSessionAction, MediaSessionActionHandler][] = [
    ['play', () => st().setPlaying(true)],
    ['pause', () => st().setPlaying(false)],
    ['previoustrack', () => st().prev()],
    ['nexttrack', () => st().next()],
    ['seekto', (d) => d.seekTime !== undefined && seekAudio(d.seekTime)],
    ['seekbackward', (d) => seekAudio((audio?.currentTime ?? 0) - (d.seekOffset ?? 10))],
    ['seekforward', (d) => seekAudio((audio?.currentTime ?? 0) + (d.seekOffset ?? 10))],
  ]
  for (const [action, fn] of handlers) {
    try {
      ms.setActionHandler(action, fn)
    } catch {
      /* action not supported by this browser */
    }
  }
}

// --- store → element ---

let started = false

/** Start following the player store (idempotent; called from AppShell). */
export function startAudioEngine(): () => void {
  if (started || typeof window === 'undefined') return () => {}
  started = true
  installSessionHandlers()
  // the queue holds paths of the signed-in user and the element keeps
  // playing on its own: logout, session expiry (401) or another user signing
  // in on the same tab empties the player (SPEC §10.4 session boundary)
  let userID = useSessionStore.getState().user?.id ?? null
  const unsubSession = useSessionStore.subscribe((s) => {
    const id = s.user?.id ?? null
    if (id === userID) return
    userID = id
    usePlayerStore.getState().clear()
  })
  // persisted preferences (SPEC §8.18 audio.volume/repeat/shuffle)
  const prefs = usePrefsStore.getState().prefs
  const vol = prefs['audio.volume']
  const rep = prefs['audio.repeat']
  usePlayerStore.setState({
    ...(typeof vol === 'number' ? { volume: vol } : {}),
    ...(rep === 'off' || rep === 'all' || rep === 'one' ? { repeat: rep as RepeatMode } : {}),
    ...(typeof prefs['audio.shuffle'] === 'boolean' ? { shuffle: prefs['audio.shuffle'] } : {}),
  })
  const unsub = usePlayerStore.subscribe((s, prev) => {
    const track = currentTrack(s)
    if (!track) {
      if (prev.index >= 0 && audio) {
        audio.pause()
        audio.removeAttribute('src')
        audio.load()
        loadedPath = null
        const ms = session()
        if (ms) ms.metadata = null
      }
    } else if (track.path !== loadedPath) {
      void load(track, s.restartToken)
    } else if (s.restartToken !== prev.restartToken && audio) {
      loadedToken = s.restartToken
      audio.currentTime = 0
      if (s.error === null && audio.src && s.playing) void audio.play().catch(() => {})
    }
    if (audio && track && s.error === null && !loading && track.path === loadedPath) {
      if (s.playing && audio.paused && audio.src) void audio.play().catch(() => {})
      if (!s.playing && !audio.paused) audio.pause()
    }
    if (audio) {
      audio.volume = s.volume
      audio.muted = s.muted
      if (audio.playbackRate !== s.rate) audio.playbackRate = s.rate
    }
    const set = usePrefsStore.getState().set
    if (s.volume !== prev.volume) set('audio.volume', Math.round(s.volume * 100) / 100)
    if (s.repeat !== prev.repeat) set('audio.repeat', s.repeat)
    if (s.shuffle !== prev.shuffle) set('audio.shuffle', s.shuffle)
  })
  return () => {
    unsub()
    unsubSession()
    started = false
  }
}
