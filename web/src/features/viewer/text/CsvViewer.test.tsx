import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'
import { CsvParser, detectDelimiter, parseCsv, parseCsvStream } from './csv'
import { CsvViewer } from './CsvViewer'

function streamOf(chunks: string[]): ReadableStream<Uint8Array> {
  const enc = new TextEncoder()
  return new ReadableStream({
    start(c) {
      for (const ch of chunks) c.enqueue(enc.encode(ch))
      c.close()
    },
  })
}

describe('csv parser', () => {
  it('handles quotes, escaped quotes, separators and newlines inside quotes, CRLF', () => {
    expect(parseCsv('a,b,c\r\n1,"x, y","he said ""hi"""\r\n2,"multi\nline",\r\n', ',')).toEqual([
      ['a', 'b', 'c'],
      ['1', 'x, y', 'he said "hi"'],
      ['2', 'multi\nline', ''],
    ])
    expect(parseCsv('a;b\n\n1;2', ';')).toEqual([
      ['a', 'b'],
      ['1', '2'],
    ])
  })
  it('splits across arbitrary chunk boundaries identically', () => {
    const text = 'id,name\n1,"Budi ""B"" Santoso"\r\n2,"baris\r\nkedua"\n3,akhir'
    const whole = parseCsv(text, ',')
    for (let size = 1; size < 8; size++) {
      const p = new CsvParser(',')
      const rows: string[][] = []
      for (let i = 0; i < text.length; i += size) rows.push(...p.push(text.slice(i, i + size)))
      rows.push(...p.end())
      expect(rows).toEqual(whole)
    }
  })
  it('detects the delimiter', () => {
    expect(detectDelimiter('a;b;c\n1;2;3\n')).toBe(';')
    expect(detectDelimiter('a\tb\n1\t2\n')).toBe('\t')
    expect(detectDelimiter('a|b|c\n1|2|3')).toBe('|')
    expect(detectDelimiter('"x;y",b\n"1;2",3\n')).toBe(',')
    expect(detectDelimiter('plain')).toBe(',')
  })
  it('streams 100k rows and stops at the cap', async () => {
    const lines = ['id;value']
    for (let i = 0; i < 100_010; i++) lines.push(`${i};v${i}`)
    const text = lines.join('\n')
    const chunks: string[] = []
    for (let i = 0; i < text.length; i += 65536) chunks.push(text.slice(i, i + 65536))
    let progress = 0
    const out = await parseCsvStream(streamOf(chunks), {
      maxRows: 100_001,
      onProgress: () => progress++,
    })
    expect(out.delimiter).toBe(';')
    expect(out.rows).toHaveLength(100_001)
    expect(out.truncated).toBe(true)
    expect(out.rows[100_000]).toEqual(['99999', 'v99999'])
    expect(progress).toBeGreaterThan(1)
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

describe('CsvViewer', () => {
  const rows = [
    ['nama', 'umur'],
    ['Budi', '30'],
    ['Ani', '9'],
    ['Citra', '100'],
  ]
  const item = { path: '/d.csv', name: 'd.csv', size: 10, ext: 'csv' }

  it('virtualises: 100k rows render only a window of row elements', () => {
    const big = [['n']]
    for (let i = 0; i < 100_000; i++) big.push([String(i)])
    render(<CsvViewer item={item} rowsOverride={big} />)
    expect(screen.getByTestId('csv-count')).toHaveTextContent('100,000 rows')
    const rendered = document.querySelectorAll('[data-row]').length
    expect(rendered).toBeGreaterThan(10)
    expect(rendered).toBeLessThan(200)
  })

  it('header toggle and client sort', async () => {
    const user = userEvent.setup()
    render(<CsvViewer item={item} rowsOverride={rows} />)
    expect(screen.getByRole('columnheader', { name: /nama/ })).toBeInTheDocument()
    expect(screen.getByTestId('csv-count')).toHaveTextContent('3 rows')
    // numeric-aware sort on "umur"
    await user.click(screen.getByRole('button', { name: /umur/ }))
    const cells = () =>
      Array.from(document.querySelectorAll('[data-row] [role="cell"]:nth-child(2)')).map(
        (c) => c.textContent,
      )
    expect(cells()).toEqual(['Ani', 'Budi', 'Citra'])
    await user.click(screen.getByRole('button', { name: /umur/ }))
    expect(cells()).toEqual(['Citra', 'Budi', 'Ani'])
    // header off: the first row becomes data
    await user.click(screen.getByTestId('csv-header-toggle'))
    expect(screen.getByTestId('csv-count')).toHaveTextContent('4 rows')
    expect(screen.getByRole('columnheader', { name: /Column 1/ })).toBeInTheDocument()
  })
})
