import { describe, expect, it } from 'vitest'
import { codecLabel, containerType, videoVerdict, type CanPlayType } from './codec'

/** A Chromium-like browser: mp4/webm/ogg, H.264/VP8/VP9/AV1 + AAC/MP3/Opus/Vorbis/FLAC, no HEVC/Matroska/AVI/QuickTime. */
const chromium: CanPlayType = (type) => {
  const [container, codecs] = type.split(';').map((s) => s.trim())
  if (!['video/mp4', 'video/webm', 'video/ogg'].includes(container!)) return ''
  if (!codecs) return 'maybe'
  const list = codecs.replace(/^codecs="|"$/g, '').split(',')
  const ok = [
    'avc1.42E01E',
    'vp8',
    'vp09.00.10.08',
    'av01.0.05M.08',
    'theora',
    'mp4a.40.2',
    'mp4a.6B',
    'opus',
    'vorbis',
    'flac',
  ]
  return list.every((c) => ok.includes(c)) ? 'probably' : ''
}

describe('videoVerdict', () => {
  it('plays H.264/AAC mp4 and VP9/Opus webm', () => {
    expect(
      videoVerdict({ name: 'a.mp4', codecVideo: 'h264', codecAudio: 'aac' }, chromium),
    ).toEqual({
      playable: true,
      type: 'video/mp4; codecs="avc1.42E01E,mp4a.40.2"',
    })
    expect(
      videoVerdict({ name: 'a.webm', codecVideo: 'vp9', codecAudio: 'opus' }, chromium).playable,
    ).toBe(true)
    expect(videoVerdict({ name: 'a.webm', codecVideo: 'av1' }, chromium).playable).toBe(true)
    expect(
      videoVerdict({ name: 'a.webm', codecVideo: 'vp8', codecAudio: 'vorbis' }, chromium).playable,
    ).toBe(true)
  })

  it('refuses HEVC with a codec label', () => {
    expect(
      videoVerdict({ name: 'film.mp4', codecVideo: 'hevc', codecAudio: 'aac' }, chromium),
    ).toEqual({
      playable: false,
      reason: 'codec',
      label: 'HEVC (H.265)',
    })
  })

  it('refuses MKV and AVI containers before looking at codecs', () => {
    expect(videoVerdict({ name: 'film.mkv', codecVideo: 'hevc' }, chromium)).toEqual({
      playable: false,
      reason: 'container',
      label: 'MKV',
    })
    expect(
      videoVerdict({ name: 'old.AVI', codecVideo: 'mpeg4', codecAudio: 'mp3' }, chromium),
    ).toMatchObject({
      playable: false,
      reason: 'container',
      label: 'AVI',
    })
  })

  it('treats QuickTime as MP4 when the browser only knows MP4', () => {
    expect(
      videoVerdict({ name: 'iphone.MOV', codecVideo: 'h264', codecAudio: 'aac' }, chromium),
    ).toMatchObject({
      playable: true,
      type: 'video/mp4; codecs="avc1.42E01E,mp4a.40.2"',
    })
  })

  it('refuses unknown or unplayable codecs, including the audio track', () => {
    expect(videoVerdict({ name: 'a.mp4', codecVideo: 'prores' }, chromium)).toMatchObject({
      playable: false,
      label: 'ProRes',
    })
    expect(
      videoVerdict({ name: 'a.mp4', codecVideo: 'h264', codecAudio: 'ac3' }, chromium),
    ).toMatchObject({
      playable: false,
      label: 'AC-3',
    })
  })

  it('tests only the container while codecs are unknown', () => {
    expect(videoVerdict({ name: 'a.mp4' }, chromium)).toEqual({ playable: true, type: 'video/mp4' })
    expect(videoVerdict({ name: 'noext', mime: 'video/webm' }, chromium)).toEqual({
      playable: true,
      type: 'video/webm',
    })
    // a browser that plays everything (Safari with HEVC)
    expect(videoVerdict({ name: 'a.mov', codecVideo: 'hevc' }, () => 'maybe').playable).toBe(true)
  })

  it('labels and container helpers', () => {
    expect(codecLabel('h264')).toBe('H.264')
    expect(codecLabel('weird')).toBe('WEIRD')
    expect(containerType({ name: 'x.m4v' })).toBe('video/mp4')
    expect(containerType({ name: 'x.bin', mime: 'application/octet-stream' })).toBe('video/mp4')
  })
})
