import { describe, expect, it } from 'vitest'
import { splitStem, validateName } from './nameValidation'

describe('validateName', () => {
  it('mirrors fsjail.ValidateName', () => {
    expect(validateName('').error).toBe('empty')
    expect(validateName('.').error).toBe('dot')
    expect(validateName('..').error).toBe('dot')
    expect(validateName('a/b').error).toBe('slash')
    expect(validateName('a\\b').error).toBe('backslash')
    expect(validateName('a\u0000b').error).toBe('nul')
    expect(validateName('a\tb').error).toBe('control_char')
    expect(validateName('x'.repeat(256)).error).toBe('too_long')
    expect(validateName('é'.repeat(128)).error).toBe('too_long') // 2 bytes each
    expect(validateName('laporan.txt').error).toBeNull()
  })
  it('flags Windows-incompatible names as a warning only', () => {
    expect(validateName('con.txt')).toEqual({ error: null, windowsIncompatible: true })
    expect(validateName('a:b')).toEqual({ error: null, windowsIncompatible: true })
    expect(validateName('trailing.')).toEqual({ error: null, windowsIncompatible: true })
    expect(validateName('normal.txt').windowsIncompatible).toBe(false)
  })
  it('detects duplicates among siblings', () => {
    expect(validateName('b.txt', ['a.txt', 'b.txt']).error).toBe('exists')
    expect(validateName('c.txt', ['a.txt', 'b.txt']).error).toBeNull()
  })
})

describe('splitStem', () => {
  it('keeps compound extensions and dotfiles intact', () => {
    expect(splitStem('arsip.tar.gz')).toEqual({ stem: 'arsip', ext: '.tar.gz' })
    expect(splitStem('foto.JPG')).toEqual({ stem: 'foto', ext: '.JPG' })
    expect(splitStem('.env')).toEqual({ stem: '.env', ext: '' })
    expect(splitStem('README')).toEqual({ stem: 'README', ext: '' })
  })
})
