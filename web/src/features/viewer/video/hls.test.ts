import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  canOfferTranscode,
  detectHlsCaps,
  hlsPlaybackMode,
  playlistReadiness,
  withSignature,
} from './hls'

// Step 23d: pure decisions of the HLS player.

describe('hlsPlaybackMode (native vs hls.js)', () => {
  it('uses the native player on Safari/iOS (Apple WebKit)', () => {
    expect(hlsPlaybackMode({ nativeHls: 'maybe', mse: true, apple: true })).toBe('native')
    expect(hlsPlaybackMode({ nativeHls: 'probably', mse: false, apple: true })).toBe('native')
  })
  it('prefers hls.js over MSE elsewhere, even when the browser answers "maybe" (Chromium)', () => {
    expect(hlsPlaybackMode({ nativeHls: 'maybe', mse: true, apple: false })).toBe('hlsjs')
    expect(hlsPlaybackMode({ nativeHls: '', mse: true, apple: false })).toBe('hlsjs')
    expect(hlsPlaybackMode({ nativeHls: '', mse: true, apple: true })).toBe('hlsjs')
  })
  it('falls back to a native player without MSE and gives up without both', () => {
    expect(hlsPlaybackMode({ nativeHls: 'maybe', mse: false, apple: false })).toBe('native')
    expect(hlsPlaybackMode({ nativeHls: '', mse: false, apple: false })).toBe('none')
  })
})

describe('detectHlsCaps', () => {
  afterEach(() => vi.unstubAllGlobals())
  it('reads canPlayType, MediaSource.isTypeSupported(H.264/AAC) and the vendor', () => {
    const isTypeSupported = vi.fn(() => true)
    vi.stubGlobal('MediaSource', { isTypeSupported })
    vi.stubGlobal('navigator', { vendor: 'Google Inc.' })
    expect(detectHlsCaps({ canPlayType: () => '' })).toEqual({
      nativeHls: '',
      mse: true,
      apple: false,
    })
    expect(isTypeSupported).toHaveBeenCalledWith('video/mp4; codecs="avc1.4d401f,mp4a.40.2"')
  })
  it('accepts ManagedMediaSource (iOS 17.1+) and reports no MSE otherwise', () => {
    vi.stubGlobal('MediaSource', undefined)
    vi.stubGlobal('ManagedMediaSource', { isTypeSupported: () => true })
    vi.stubGlobal('navigator', { vendor: 'Apple Computer, Inc.' })
    expect(detectHlsCaps({ canPlayType: () => 'maybe' })).toEqual({
      nativeHls: 'maybe',
      mse: true,
      apple: true,
    })
    vi.stubGlobal('ManagedMediaSource', undefined)
    expect(detectHlsCaps({ canPlayType: () => '' }).mse).toBe(false)
  })
})

describe('canOfferTranscode (button visibility matrix)', () => {
  const cases: Array<[boolean, boolean | undefined, boolean, boolean]> = [
    // playable, transcodeEnabled, foreignScope → shown
    [false, true, false, true],
    [false, false, false, false],
    [false, undefined, false, false],
    [true, true, false, false],
    [true, false, false, false],
    [false, true, true, false], // guest page / received share / admin browse
  ]
  it.each(cases)('playable=%s enabled=%s foreign=%s → %s', (playable, enabled, foreign, want) => {
    expect(
      canOfferTranscode({
        verdict: { playable },
        transcodeEnabled: enabled,
        foreignScope: foreign,
      }),
    ).toBe(want)
  })
  it('also offers it after a decode failure of a "playable" file, only when enabled', () => {
    const base = { verdict: { playable: true }, decodeFailed: true, foreignScope: false }
    expect(canOfferTranscode({ ...base, transcodeEnabled: true })).toBe(true)
    expect(canOfferTranscode({ ...base, transcodeEnabled: false })).toBe(false)
    expect(canOfferTranscode({ verdict: null, transcodeEnabled: true, foreignScope: false })).toBe(
      false,
    )
  })
})

describe('playlistReadiness', () => {
  const head = '#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:EVENT\n'
  const seg = (n: number) => `#EXTINF:4.000000,\nseg_${String(n).padStart(5, '0')}.ts?sig=x\n`
  it('waits for two segments of a running EVENT playlist', () => {
    expect(playlistReadiness(head)).toEqual({ segments: 0, ended: false, ready: false })
    expect(playlistReadiness(head + seg(0))).toEqual({ segments: 1, ended: false, ready: false })
    expect(playlistReadiness(head + seg(0) + seg(1))).toEqual({
      segments: 2,
      ended: false,
      ready: true,
    })
  })
  it('plays a short finished list with a single segment', () => {
    expect(playlistReadiness(head + seg(0) + '#EXT-X-ENDLIST\n')).toEqual({
      segments: 1,
      ended: true,
      ready: true,
    })
    expect(playlistReadiness(head.replace(/\n/g, '\r\n') + '#EXT-X-ENDLIST\r\n').ready).toBe(false)
  })
  it('rejects anything that is not a playlist', () => {
    expect(playlistReadiness('').ready).toBe(false)
    expect(playlistReadiness('{"error":{}}').ready).toBe(false)
  })
})

describe('withSignature', () => {
  const fresh = '/api/v1/files/hls/T/playlist.m3u8?exp=200&scope=u%3Aabc&sig=NEW'
  it('replaces the folder signature of segment and playlist URLs', () => {
    expect(
      withSignature('/api/v1/files/hls/T/seg_00003.ts?exp=100&scope=u%3Aabc&sig=OLD', fresh),
    ).toBe('/api/v1/files/hls/T/seg_00003.ts?exp=200&scope=u%3Aabc&sig=NEW')
    const abs = `${window.location.origin}/api/v1/files/hls/T/playlist.m3u8?exp=1&scope=u%3Aabc&sig=a`
    expect(withSignature(abs, fresh)).toBe(fresh)
  })
  it('adds the signature to a URL without one', () => {
    expect(withSignature('/api/v1/files/hls/T/seg_00000.ts', fresh)).toBe(
      '/api/v1/files/hls/T/seg_00000.ts?scope=u%3Aabc&exp=200&sig=NEW',
    )
  })
})
