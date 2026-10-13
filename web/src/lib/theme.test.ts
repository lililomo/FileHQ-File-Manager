import { beforeEach, describe, expect, it } from 'vitest'
import { applyAccent, applyTheme, resolveTheme } from './theme'

describe('theme helpers', () => {
  beforeEach(() => {
    localStorage.clear()
    delete document.documentElement.dataset.theme
    delete document.documentElement.dataset.accent
  })
  it('applies and persists the theme preference', () => {
    expect(applyTheme('dark')).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(document.documentElement.dataset.themePref).toBe('dark')
    expect(localStorage.getItem('theme')).toBe('dark')
    expect(resolveTheme('system')).toBe('light') // jsdom matchMedia stub → light
  })
  it('stores non-default accents only', () => {
    applyAccent('violet')
    expect(document.documentElement.dataset.accent).toBe('violet')
    expect(localStorage.getItem('accent')).toBe('violet')
    applyAccent('blue')
    expect(document.documentElement.dataset.accent).toBeUndefined()
    expect(localStorage.getItem('accent')).toBeNull()
  })
})
