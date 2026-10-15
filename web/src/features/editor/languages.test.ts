import { describe, expect, it } from 'vitest'
import { detectLanguage, isEditableByName, LANGUAGE_OPTIONS } from './languages'

describe('detectLanguage', () => {
  it('maps extensions, special names, compound names and shebangs', () => {
    expect(detectLanguage('app.ts')).toBe('typescript')
    expect(detectLanguage('Dockerfile')).toBe('dockerfile')
    expect(detectLanguage('Makefile')).toBe('makefile')
    expect(detectLanguage('.env.production')).toBe('ini')
    expect(detectLanguage('nginx.conf')).toBe('nginx')
    expect(detectLanguage('default.conf')).toBe('ini')
    expect(detectLanguage('.htaccess')).toBe('apache')
    expect(detectLanguage('home.blade.php')).toBe('php')
    expect(detectLanguage('composer.json')).toBe('json')
    expect(detectLanguage('run', '#!/usr/bin/env python3')).toBe('python')
    expect(detectLanguage('run.txt', '#!/bin/bash')).toBe('shell')
    expect(detectLanguage('noext')).toBe('plaintext')
  })
  it('offers every custom grammar in the picker', () => {
    const ids = LANGUAGE_OPTIONS.map((l) => l.id)
    expect(ids).toEqual(expect.arrayContaining(['nginx', 'apache', 'makefile', 'ini']))
  })
  it('decides what "Buka" sends to the editor', () => {
    expect(isEditableByName('a.txt', 'text/plain', 'text')).toBe(true)
    expect(isEditableByName('a.go', null, 'code')).toBe(true)
    expect(isEditableByName('x.bin', null, 'other')).toBe(false)
    expect(isEditableByName('photo.jpg', 'image/jpeg', 'image')).toBe(false)
    expect(isEditableByName('data.json', 'application/json', 'other')).toBe(true)
  })
})
