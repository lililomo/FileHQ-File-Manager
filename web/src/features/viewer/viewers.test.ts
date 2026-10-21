import { describe, expect, it } from 'vitest'
import {
  imageSourceFor,
  opensInViewer,
  sameViewerKind,
  viewerFor,
  type ViewerItem,
} from './viewers'

const file = (name: string, mime: string | null, category: string, ext?: string): ViewerItem => ({
  name,
  mime,
  category,
  ext: ext ?? (name.includes('.') ? name.split('.').pop()! : ''),
  isDir: false,
})

describe('viewerFor (SPEC §8.14 MIME → viewer)', () => {
  it('maps raster images, svg/tiff/heic and pdf', () => {
    for (const n of ['a.jpg', 'b.jpeg', 'c.png', 'd.gif', 'e.webp', 'f.avif', 'g.bmp', 'h.ico'])
      expect(viewerFor(file(n, null, 'image'))).toBe('image')
    expect(viewerFor(file('v.svg', 'image/svg+xml', 'image'))).toBe('image')
    expect(viewerFor(file('t.tiff', 'image/tiff', 'image'))).toBe('image')
    expect(viewerFor(file('p.heic', null, 'image'))).toBe('image')
    expect(viewerFor(file('doc.pdf', 'application/pdf', 'pdf'))).toBe('pdf')
    expect(viewerFor(file('noext', 'application/pdf', 'other', ''))).toBe('pdf')
  })
  it('groups the later viewers by kind and falls back to info', () => {
    expect(viewerFor(file('s.mp3', 'audio/mpeg', 'audio'))).toBe('audio')
    expect(viewerFor(file('m.mp4', 'video/mp4', 'video'))).toBe('video')
    expect(viewerFor(file('r.md', 'text/markdown', 'text'))).toBe('markdown')
    expect(viewerFor(file('d.csv', 'text/csv', 'text'))).toBe('table')
    expect(viewerFor(file('c.json', 'application/json', 'code'))).toBe('json')
    expect(viewerFor(file('x.ts', null, 'code'))).toBe('text')
    expect(viewerFor(file('a.zip', 'application/zip', 'archive'))).toBe('archive')
    expect(viewerFor(file('w.docx', null, 'document'))).toBe('info')
    expect(viewerFor(file('bin', null, 'other', ''))).toBe('info')
    expect(viewerFor({ ...file('x.jpg', null, 'image'), isDir: true })).toBe('info')
  })
  it('image, pdf, audio and video open in the overlay (17b-2)', () => {
    expect(opensInViewer(file('a.jpg', null, 'image'))).toBe(true)
    expect(opensInViewer(file('a.pdf', null, 'pdf'))).toBe(true)
    expect(opensInViewer(file('a.mp3', null, 'audio'))).toBe(true)
    expect(opensInViewer(file('a.mkv', null, 'video'))).toBe(true)
    expect(opensInViewer(file('a.txt', 'text/plain', 'text'))).toBe(false)
  })
  it('sibling navigation stays within one kind', () => {
    expect(sameViewerKind(file('a.jpg', null, 'image'), file('b.png', null, 'image'))).toBe(true)
    expect(sameViewerKind(file('a.jpg', null, 'image'), file('b.pdf', null, 'pdf'))).toBe(false)
  })
})

describe('imageSourceFor', () => {
  it('serves gif animated, svg/tiff/heic raster-only, the rest direct', () => {
    expect(imageSourceFor(file('a.gif', 'image/gif', 'image'))).toBe('animated')
    expect(imageSourceFor(file('a.svg', 'image/svg+xml', 'image'))).toBe('raster')
    expect(imageSourceFor(file('a.heic', null, 'image'))).toBe('raster')
    expect(imageSourceFor(file('a.tif', null, 'image'))).toBe('raster')
    expect(imageSourceFor(file('a.jpg', 'image/jpeg', 'image'))).toBe('direct')
  })
})
