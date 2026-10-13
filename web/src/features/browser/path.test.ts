import { describe, expect, it } from 'vitest'
import {
  baseName,
  joinPath,
  normalizeTypedPath,
  parentPath,
  pathToSplat,
  splatToPath,
  splitPath,
} from './path'

describe('path helpers', () => {
  it('splits and joins', () => {
    expect(splitPath('/a/b')).toEqual(['a', 'b'])
    expect(splitPath('/')).toEqual([])
    expect(joinPath('/', 'x')).toBe('/x')
    expect(joinPath('/a', 'x')).toBe('/a/x')
    expect(baseName('/a/b.txt')).toBe('b.txt')
  })
  it('parent of root is null', () => {
    expect(parentPath('/')).toBeNull()
    expect(parentPath('/a')).toBe('/')
    expect(parentPath('/a/b')).toBe('/a')
  })
  it('converts splats', () => {
    expect(splatToPath(undefined)).toBe('/')
    expect(splatToPath('a/b/')).toBe('/a/b')
    expect(pathToSplat('/a/b')).toBe('a/b')
    expect(pathToSplat('/')).toBe('')
  })
  it('normalises typed paths', () => {
    expect(normalizeTypedPath(' /a//b/ ')).toEqual({ ok: true, path: '/a/b' })
    expect(normalizeTypedPath('sub', '/a')).toEqual({ ok: true, path: '/a/sub' })
    expect(normalizeTypedPath('./x/./y')).toEqual({ ok: true, path: '/x/y' })
    expect(normalizeTypedPath('')).toEqual({ ok: true, path: '/' })
    expect(normalizeTypedPath('/a/../b')).toEqual({ ok: false, reason: 'parent_segment' })
    expect(normalizeTypedPath('/a\u0000')).toEqual({ ok: false, reason: 'control_char' })
    expect(normalizeTypedPath('/.system/x')).toEqual({ ok: false, reason: 'reserved' })
    expect(normalizeTypedPath('/' + 'x'.repeat(256))).toEqual({
      ok: false,
      reason: 'name_too_long',
    })
  })
})
