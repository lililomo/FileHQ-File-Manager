import { describe, expect, it } from 'vitest'
import { groupMatches } from './grouping'
import { DEFAULT_FILTERS } from './filters'
import { toContentQuery, toNameFilters } from './query'

describe('search query helpers', () => {
  it('maps the filter form to name search params (KB → bytes, defaults omitted)', () => {
    expect(toNameFilters({ ...DEFAULT_FILTERS })).toEqual({})
    expect(
      toNameFilters({
        ...DEFAULT_FILTERS,
        recursive: false,
        type: 'pdf',
        ext: ' pdf ',
        minKB: '2',
        maxKB: '0',
        tag: 'red',
        glob: true,
      }),
    ).toEqual({
      recursive: false,
      glob: true,
      types: ['pdf'],
      ext: 'pdf',
      minSize: 2048,
      tag: 'red',
    })
  })
  it('maps the filter form to a content query (ext list split, flags kept)', () => {
    expect(
      toContentQuery('x', '/a', {
        ...DEFAULT_FILTERS,
        ext: '.txt, md,,',
        wholeWord: true,
        from: '2026-01-01',
      }),
    ).toEqual({
      q: 'x',
      path: '/a',
      recursive: true,
      regex: false,
      caseSensitive: false,
      wholeWord: true,
      ext: ['txt', 'md'],
      from: '2026-01-01',
    })
  })
  it('groups matches per file keeping arrival order', () => {
    const g = groupMatches([
      { file: '/b', line: 1, col: 1, text: 'x' },
      { file: '/a', line: 3, col: 2, text: 'y' },
      { file: '/b', line: 2, col: 1, text: 'z' },
    ])
    expect(g.map((x) => x.file)).toEqual(['/b', '/a'])
    expect(g[0]!.matches).toHaveLength(2)
  })
})
