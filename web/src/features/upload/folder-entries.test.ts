import { describe, expect, it } from 'vitest'
import {
  entriesFromClipboard,
  entriesFromDataTransfer,
  entriesFromFileList,
  MAX_ENTRIES,
  pastedImageName,
  relativeDir,
} from './folder-entries'

function fileWithRel(name: string, rel: string): File {
  const f = new File(['x'], name, { type: 'text/plain' })
  Object.defineProperty(f, 'webkitRelativePath', { value: rel })
  return f
}

describe('folder-entries', () => {
  it('strips the file name from webkitRelativePath', () => {
    expect(relativeDir('a/b/c/file.txt')).toBe('a/b/c')
    expect(relativeDir('file.txt')).toBe('')
    expect(relativeDir('a\\b\\file.txt')).toBe('a/b')
    expect(relativeDir('/a/')).toBe('')
  })

  it('keeps the structure of a webkitdirectory pick', () => {
    const { entries, truncated } = entriesFromFileList([
      fileWithRel('f.txt', 'top/sub/f.txt'),
      fileWithRel('g.txt', 'top/g.txt'),
      new File(['x'], 'plain.txt'),
    ])
    expect(truncated).toBe(false)
    expect(entries.map((e) => [e.file.name, e.relativePath])).toEqual([
      ['f.txt', 'top/sub'],
      ['g.txt', 'top'],
      ['plain.txt', ''],
    ])
  })

  it('caps a batch at MAX_ENTRIES', () => {
    const files = Array.from({ length: MAX_ENTRIES + 5 }, (_, i) => new File(['x'], `f${i}.txt`))
    const { entries, truncated } = entriesFromFileList(files)
    expect(entries).toHaveLength(MAX_ENTRIES)
    expect(truncated).toBe(true)
  })

  it('walks dropped directory entries recursively (readEntries in batches)', async () => {
    type Entry = {
      isFile: boolean
      isDirectory: boolean
      name: string
      file?: (ok: (f: File) => void) => void
      createReader?: () => { readEntries: (ok: (es: Entry[]) => void) => void }
    }
    const fileEntry = (name: string): Entry => ({
      isFile: true,
      isDirectory: false,
      name,
      file: (ok) => ok(new File(['x'], name)),
    })
    const dirEntry = (name: string, children: Entry[]): Entry => {
      let served = false
      return {
        isFile: false,
        isDirectory: true,
        name,
        createReader: () => ({
          readEntries: (ok) => {
            if (served) ok([])
            else {
              served = true
              ok(children)
            }
          },
        }),
      }
    }
    const tree = dirEntry('a', [
      fileEntry('root.txt'),
      dirEntry('b', [dirEntry('c', [fileEntry('deep.txt')])]),
    ])
    const dt = {
      items: [{ kind: 'file', webkitGetAsEntry: () => tree }],
      files: [],
    } as unknown as DataTransfer
    const { entries } = await entriesFromDataTransfer(dt)
    expect(entries.map((e) => [e.file.name, e.relativePath])).toEqual([
      ['root.txt', 'a'],
      ['deep.txt', 'a/b/c'],
    ])
  })

  it('falls back to dt.files without the entry API', async () => {
    const dt = { items: [], files: [new File(['x'], 'p.txt')] } as unknown as DataTransfer
    const { entries } = await entriesFromDataTransfer(dt)
    expect(entries.map((e) => e.file.name)).toEqual(['p.txt'])
  })

  it('names pasted images image-YYYYMMDD-HHMMSS.ext', () => {
    const now = new Date(2026, 9, 14, 7, 5, 9)
    expect(pastedImageName(new File([''], 'image.png', { type: 'image/png' }), now)).toBe(
      'image-20261014-070509.png',
    )
    expect(pastedImageName(new File([''], 'image.jpeg', { type: 'image/jpeg' }), now)).toBe(
      'image-20261014-070509.jpg',
    )
    const dt = {
      files: [
        new File(['x'], 'image.png', { type: 'image/png' }),
        new File(['y'], 'laporan.pdf', { type: 'application/pdf' }),
      ],
      items: [],
    } as unknown as DataTransfer
    const entries = entriesFromClipboard(dt, now)
    expect(entries.map((e) => e.file.name)).toEqual(['image-20261014-070509.png', 'laporan.pdf'])
  })
})
