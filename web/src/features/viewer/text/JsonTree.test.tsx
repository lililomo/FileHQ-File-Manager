import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { TooltipProvider } from '@/components/ui/tooltip'
import { JsonTree } from './JsonTree'
import {
  ancestors,
  displayPath,
  expandToDepth,
  flatten,
  searchKeys,
  type JsonValue,
} from './jsonTree'

const doc: JsonValue = {
  nama: 'proyek',
  versi: 3,
  aktif: true,
  kosong: null,
  tim: [
    { nama: 'Budi', peran: 'dev' },
    { nama: 'Ani', 'kunci aneh/~': 1 },
  ],
  config: { deep: { deeper: { target: 'ok' } } },
}

describe('json tree model', () => {
  it('flattens only expanded containers, depth-first in document order', () => {
    const rows = flatten(doc, new Set(['']))
    expect(rows.map((r) => r.key)).toEqual([
      null,
      'nama',
      'versi',
      'aktif',
      'kosong',
      'tim',
      'config',
    ])
    expect(rows.find((r) => r.key === 'tim')).toMatchObject({ container: true, size: 2, depth: 1 })
    const two = flatten(doc, expandToDepth(doc, 2))
    expect(two.map((r) => r.ptr)).toContain('/tim/1')
    expect(two.map((r) => r.ptr)).not.toContain('/tim/1/nama')
  })
  it('searches keys case-insensitively and reveals through ancestors', () => {
    expect(searchKeys(doc, 'NAMA')).toEqual(['/nama', '/tim/0/nama', '/tim/1/nama'])
    expect(searchKeys(doc, 'target')).toEqual(['/config/deep/deeper/target'])
    expect(ancestors('/config/deep/deeper/target')).toEqual([
      '',
      '/config',
      '/config/deep',
      '/config/deep/deeper',
    ])
    expect(searchKeys(doc, ' ')).toEqual([])
  })
  it('escapes pointers and shows JSONPath', () => {
    const p = searchKeys(doc, 'aneh')[0]!
    expect(p).toBe('/tim/1/kunci aneh~1~0')
    expect(displayPath(p)).toBe('$.tim[1]["kunci aneh/~"]')
    expect(displayPath('')).toBe('$')
  })
})

// jsdom has no layout: give the virtualised scroller a viewport (TanStack
// Virtual reads offsetWidth/offsetHeight)
const proto = HTMLElement.prototype
const realW = Object.getOwnPropertyDescriptor(proto, 'offsetWidth')!
const realH = Object.getOwnPropertyDescriptor(proto, 'offsetHeight')!
beforeAll(() => {
  Object.defineProperty(proto, 'offsetWidth', { configurable: true, get: () => 1024 })
  Object.defineProperty(proto, 'offsetHeight', { configurable: true, get: () => 768 })
})
afterAll(() => {
  Object.defineProperty(proto, 'offsetWidth', realW)
  Object.defineProperty(proto, 'offsetHeight', realH)
})

describe('JsonTree', () => {
  it('expands, collapses and finds keys', async () => {
    const user = userEvent.setup()
    render(
      <TooltipProvider>
        <JsonTree value={doc} />
      </TooltipProvider>,
    )
    const tree = screen.getByRole('tree')
    // depth 2 open by default: tim[0] visible, its fields not yet
    expect(within(tree).getAllByRole('treeitem').length).toBe(10)
    expect(tree.querySelector('[data-ptr="/tim/0/peran"]')).toBeNull()
    // expand tim[0]
    const row = tree.querySelector('[data-ptr="/tim/0"]') as HTMLElement
    await user.click(within(row).getByTestId('json-toggle'))
    expect(tree.querySelector('[data-ptr="/tim/0/peran"]')).not.toBeNull()
    // collapse all → only the root row
    await user.click(screen.getByTestId('json-collapse-all'))
    expect(within(tree).getAllByRole('treeitem')).toHaveLength(1)
    // search a deep key: ancestors open, match highlighted, counter shown
    await user.type(screen.getByTestId('json-search'), 'target')
    expect(await screen.findByTestId('json-matches')).toHaveTextContent('1 of 1')
    expect(tree.querySelector('[data-ptr="/config/deep/deeper/target"]')).not.toBeNull()
    await user.clear(screen.getByTestId('json-search'))
    await user.type(screen.getByTestId('json-search'), 'tidak-ada')
    expect(screen.getByTestId('json-matches')).toHaveTextContent('No matches')
  })
})
