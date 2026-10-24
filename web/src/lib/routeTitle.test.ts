import { describe, expect, it } from 'vitest'
import { titleParts } from './routeTitle'

const t = (k: string) => `[${k}]`

describe('titleParts', () => {
  it('names folders, sections and admin pages', () => {
    expect(titleParts('/files', t)).toEqual(['[nav:files]'])
    expect(titleParts('/files/Proyek/laporan%202026', t)).toEqual(['laporan 2026', '[nav:files]'])
    expect(titleParts('/trash', t)).toEqual(['[nav:trash]'])
    expect(titleParts('/admin/users', t)).toEqual(['[nav:adminUsers]', '[nav:admin]'])
    expect(titleParts('/admin', t)).toEqual(['[nav:admin]'])
    expect(titleParts('/s/abc', t)).toEqual([])
    expect(titleParts('/login', t)).toEqual([])
  })
})
