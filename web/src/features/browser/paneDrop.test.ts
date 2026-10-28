import { describe, expect, it } from 'vitest'
import { resolvePaneDrop, transferablePaths } from './paneDrop'

describe('pane drop resolution (Step 23b)', () => {
  const base = { dir: '/b', overFolderRow: false, copy: false }
  it('moves by default and copies with Ctrl', () => {
    expect(resolvePaneDrop({ ...base, paths: ['/a/x.txt'] })).toEqual({
      op: 'move',
      paths: ['/a/x.txt'],
    })
    expect(resolvePaneDrop({ ...base, paths: ['/a/x.txt'], copy: true })?.op).toBe('copy')
  })
  it('is a no-op for items already in the folder', () => {
    expect(resolvePaneDrop({ ...base, paths: ['/b/x.txt'] })).toBeNull()
    expect(transferablePaths(['/b/x.txt', '/a/y.txt'], '/b')).toEqual(['/a/y.txt'])
  })
  it('leaves folder rows and foreign drags to their own targets', () => {
    expect(resolvePaneDrop({ ...base, paths: ['/a/x.txt'], overFolderRow: true })).toBeNull()
    expect(resolvePaneDrop({ ...base, paths: null })).toBeNull()
    expect(resolvePaneDrop({ ...base, paths: [] })).toBeNull()
  })
  it('refuses a folder dropped into itself or its subtree', () => {
    expect(resolvePaneDrop({ ...base, dir: '/a', paths: ['/a'] })).toBeNull()
    expect(resolvePaneDrop({ ...base, dir: '/a/sub', paths: ['/a'] })).toBeNull()
    expect(resolvePaneDrop({ ...base, dir: '/', paths: ['/a/sub'] })).toEqual({
      op: 'move',
      paths: ['/a/sub'],
    })
  })
})
