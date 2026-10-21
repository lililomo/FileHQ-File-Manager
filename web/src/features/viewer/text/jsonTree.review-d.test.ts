import { describe, expect, it } from 'vitest'
import { allContainers, expandToDepth, flatten, searchKeys } from './jsonTree'

// Review D: a pathologically nested document (20 000 levels) used to throw
// RangeError from the recursive walkers during render — with no error
// boundary the whole application unmounted. The walkers are iterative now
// and keep document order.
describe('jsonTree review D', () => {
  const depth = 20_000
  const deep = JSON.parse('{"k":'.repeat(depth) + '1' + '}'.repeat(depth)) as never

  it('flatten / expand all / searchKeys survive 20k nesting', () => {
    expect(() => flatten(deep, expandToDepth(deep, 2))).not.toThrow()
    const all = allContainers(deep)
    expect(all.size).toBe(depth)
    const rows = flatten(deep, all)
    expect(rows.length).toBe(depth + 1)
    expect(rows[depth]!.value).toBe(1)
    expect(searchKeys(deep, 'k').length).toBe(1000) // capped
  })

  it('keeps document order', () => {
    const doc = { b: { y: 1, x: { k: 2 } }, a: [{ k: 3 }, 4], k: 5 } as never
    const rows = flatten(doc, allContainers(doc))
    expect(rows.map((r) => r.ptr)).toEqual([
      '',
      '/b',
      '/b/y',
      '/b/x',
      '/b/x/k',
      '/a',
      '/a/0',
      '/a/0/k',
      '/a/1',
      '/k',
    ])
    expect(searchKeys(doc, 'k')).toEqual(['/k', '/b/x/k', '/a/0/k'].sort(byDoc(rows)))
    expect(expandToDepth(doc, 1)).toEqual(new Set(['']))
    expect(expandToDepth(doc, 2)).toEqual(new Set(['', '/b', '/a']))
  })
})

function byDoc(rows: { ptr: string }[]) {
  const idx = new Map(rows.map((r, i) => [r.ptr, i]))
  return (a: string, b: string) => idx.get(a)! - idx.get(b)!
}
