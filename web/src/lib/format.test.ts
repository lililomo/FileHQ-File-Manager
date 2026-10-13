import { describe, expect, it } from 'vitest'
import { formatBytes, formatDate, formatRelativeTime } from './format'

describe('formatBytes', () => {
  it('formats with a comma decimal in Indonesian', () => {
    expect(formatBytes(1.2 * 1024 * 1024, 'id')).toBe('1,2 MB')
    expect(formatBytes(24 * 1024, 'id')).toBe('24 KB')
    expect(formatBytes(0, 'id')).toBe('0 B')
    expect(formatBytes(6.2 * 1024 ** 3, 'id')).toBe('6,2 GB')
    expect(formatBytes(10 * 1024 ** 3, 'id')).toBe('10 GB')
    expect(formatBytes(999, 'id')).toBe('999 B')
  })
  it('formats with a dot decimal in English', () => {
    expect(formatBytes(1.2 * 1024 * 1024, 'en')).toBe('1.2 MB')
    expect(formatBytes(1536, 'en')).toBe('1.5 KB')
  })
  it('rejects invalid input', () => {
    expect(formatBytes(-1, 'id')).toBe('—')
    expect(formatBytes(Number.NaN, 'id')).toBe('—')
  })
})

describe('formatDate', () => {
  const d = new Date(Date.UTC(2026, 9, 12, 7, 5, 0))
  it('uses the id-ID short form', () => {
    expect(formatDate(d, 'id', { timeZone: 'UTC' })).toBe('12 Okt 2026')
    expect(formatDate(d, 'id', { time: true, timeZone: 'UTC' })).toBe('12 Okt 2026, 07.05')
  })
  it('uses day-month-year in English', () => {
    expect(formatDate(d, 'en', { timeZone: 'UTC' })).toBe('12 Oct 2026')
    expect(formatDate(d, 'en', { time: true, timeZone: 'UTC' })).toBe('12 Oct 2026, 07:05')
  })
  it('returns a dash for invalid dates', () => {
    expect(formatDate('not-a-date', 'id')).toBe('—')
  })
})

describe('formatRelativeTime', () => {
  const now = Date.UTC(2026, 9, 12, 12, 0, 0)
  it('is relative within a week', () => {
    expect(formatRelativeTime(now - 2 * 3600_000, 'id', now)).toBe('2 jam yang lalu')
    expect(formatRelativeTime(now - 2 * 3600_000, 'en', now)).toBe('2 hours ago')
    expect(formatRelativeTime(now - 86_400_000, 'id', now)).toBe('kemarin')
    expect(formatRelativeTime(now - 86_400_000, 'en', now)).toBe('yesterday')
    expect(formatRelativeTime(now - 10_000, 'id', now)).toBe('sekarang')
  })
  it('falls back to the absolute date after a week', () => {
    expect(formatRelativeTime(now - 10 * 86_400_000, 'id', now)).toMatch(/2026$/)
  })
})
