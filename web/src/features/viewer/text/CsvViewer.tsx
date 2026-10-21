import { ArrowDown, ArrowUp, Info } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Switch } from '@/components/ui/switch'
import type { Item } from '@/features/browser/api'
import { fetchSigned } from '@/lib/media-url'
import { formatNumber } from '@/lib/format'
import { parseCsvStream } from './csv'
import { TextState } from './TextState'
import { VirtualRows } from './VirtualRows'

/** Rows shown at most (SPEC §8.14: 100.000). */
export const CSV_MAX_ROWS = 100_000
/** Bytes read at most (SPEC §8.14: ≤ 20 MB); longer files are cut. */
export const CSV_MAX_BYTES = 20 * 1024 * 1024
const ROW = 28
const DEFAULT_WIDTH = 160
const NUM_WIDTH = 64

type Sort = { col: number; dir: 1 | -1 } | null

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

/** CSV/TSV as a virtualised table (SPEC §8.14 TableViewer, PROMPT 17b):
 *  streamed from the signed raw URL and parsed while it arrives, delimiter
 *  detected, first row as header (toggle), click a header to sort, drag a
 *  header edge to resize. `rowsOverride` feeds rows directly (tests). */
export function CsvViewer({
  item,
  rowsOverride,
}: {
  item: Pick<Item, 'path' | 'name' | 'size' | 'ext'>
  rowsOverride?: string[][]
}) {
  const { t, i18n } = useTranslation('viewer')
  const lang = i18n.resolvedLanguage ?? 'id'
  const [rows, setRows] = useState<string[][]>(rowsOverride ?? [])
  const [status, setStatus] = useState<'loading' | 'streaming' | 'done' | 'error'>(
    rowsOverride ? 'done' : 'loading',
  )
  const [truncated, setTruncated] = useState(false)
  const [delimiter, setDelimiter] = useState(',')
  const [header, setHeader] = useState(true)
  const [sort, setSort] = useState<Sort>(null)
  const [widths, setWidths] = useState<Record<number, number>>({})

  useEffect(() => {
    if (rowsOverride) return
    const ctrl = new AbortController()
    let last = 0
    ;(async () => {
      const res = await fetchSigned(item.path, 'media', {
        headers: { Range: `bytes=0-${CSV_MAX_BYTES - 1}` },
        signal: ctrl.signal,
      })
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`)
      const cut = res.status === 206 && item.size > CSV_MAX_BYTES
      const out = await parseCsvStream(res.body, {
        maxRows: CSV_MAX_ROWS + 1,
        ...(item.ext?.toLowerCase() === 'tsv' ? { delimiter: '\t' } : {}),
        signal: ctrl.signal,
        onProgress: (r) => {
          // re-render at most every 250 ms while streaming
          const now = performance.now()
          if (now - last < 250) return
          last = now
          setRows(r.slice())
          setStatus('streaming')
        },
      })
      setRows(out.rows)
      setDelimiter(out.delimiter)
      setTruncated(out.truncated || cut)
      setStatus('done')
    })().catch((e: unknown) => {
      if ((e as Error)?.name !== 'AbortError') setStatus('error')
    })
    return () => ctrl.abort()
  }, [item.path, item.size, item.ext, rowsOverride])

  const head = header ? (rows[0] ?? []) : []
  const body = useMemo(() => {
    const data = header ? rows.slice(1) : rows
    const capped = data.length > CSV_MAX_ROWS ? data.slice(0, CSV_MAX_ROWS) : data
    if (!sort) return capped
    const idx = capped.map((_, i) => i)
    idx.sort(
      (a, b) => sort.dir * collator.compare(capped[a]![sort.col] ?? '', capped[b]![sort.col] ?? ''),
    )
    return idx.map((i) => capped[i]!)
  }, [rows, header, sort])
  const cols = useMemo(
    () => rows.reduce((m, r, i) => (i < 2000 ? Math.max(m, r.length) : m), 0),
    [rows],
  )
  const width = (c: number) => widths[c] ?? DEFAULT_WIDTH
  const total =
    NUM_WIDTH + Array.from({ length: cols }, (_, c) => width(c)).reduce((a, b) => a + b, 0)

  if (status === 'loading') return <TextState kind="loading" />
  if (status === 'error') return <TextState kind="error" />

  const label = (c: number) =>
    header ? head[c] || t('csv.column', { n: c + 1 }) : t('csv.column', { n: c + 1 })
  const delimName = delimiter === '\t' ? 'TAB' : delimiter
  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="csv-viewer">
      <div className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-surface px-3 text-xs text-text-muted">
        <span className="tabular" data-testid="csv-count">
          {t('csv.rows', { count: body.length, n: formatNumber(body.length, lang) })} ·{' '}
          {t('csv.columns', { count: cols })}
        </span>
        <span>{t('csv.delimiter', { d: delimName })}</span>
        {status === 'streaming' ? <span aria-live="polite">{t('csv.loadingMore')}</span> : null}
        <span className="flex-1" />
        <label className="flex items-center gap-2">
          <Switch
            checked={header}
            onCheckedChange={(v) => {
              setHeader(v)
              setSort(null)
            }}
            data-testid="csv-header-toggle"
          />
          {t('csv.header')}
        </label>
      </div>
      {truncated ? (
        <div
          className="flex shrink-0 items-center gap-2 border-b border-border bg-surface-2 px-3 py-1.5 text-xs text-text-muted"
          role="note"
        >
          <Info aria-hidden="true" className="size-3.5" />
          {t('csv.truncated', { n: formatNumber(CSV_MAX_ROWS, lang) })}
        </div>
      ) : null}
      <VirtualRows
        count={body.length}
        rowHeight={ROW}
        minWidth={total}
        testId="csv-scroll"
        role="table"
        label={item.name}
        className="bg-bg text-xs"
        header={
          <div
            className="sticky top-0 z-10 flex border-b border-border bg-surface-2 font-medium text-text"
            role="row"
            style={{ height: ROW }}
          >
            <span
              className="shrink-0 border-r border-border px-2 py-1.5 text-right text-text-muted"
              style={{ width: NUM_WIDTH }}
              role="columnheader"
            >
              #
            </span>
            {Array.from({ length: cols }, (_, c) => (
              <HeaderCell
                key={c}
                label={label(c)}
                width={width(c)}
                sort={sort?.col === c ? sort.dir : 0}
                onSort={() =>
                  setSort((s) =>
                    s?.col === c ? (s.dir === 1 ? { col: c, dir: -1 } : null) : { col: c, dir: 1 },
                  )
                }
                onResize={(w) => setWidths((m) => ({ ...m, [c]: w }))}
              />
            ))}
          </div>
        }
        renderRow={(i) => {
          const r = body[i]!
          return (
            <div
              className="flex h-full border-b border-border/60 hover:bg-surface-2"
              role="row"
              data-row={i}
            >
              <span
                className="shrink-0 border-r border-border px-2 py-1.5 text-right text-text-muted tabular"
                style={{ width: NUM_WIDTH }}
                role="cell"
              >
                {i + 1}
              </span>
              {Array.from({ length: cols }, (_, c) => (
                <span
                  key={c}
                  className="shrink-0 truncate border-r border-border/60 px-2 py-1.5"
                  style={{ width: width(c) }}
                  title={r[c]}
                  role="cell"
                >
                  {r[c] ?? ''}
                </span>
              ))}
            </div>
          )
        }}
      />
    </div>
  )
}

function HeaderCell({
  label,
  width,
  sort,
  onSort,
  onResize,
}: {
  label: string
  width: number
  sort: 0 | 1 | -1
  onSort: () => void
  onResize: (w: number) => void
}) {
  const { t } = useTranslation('viewer')
  const start = useRef<{ x: number; w: number } | null>(null)
  return (
    <span
      className="relative flex shrink-0 items-center border-r border-border"
      style={{ width }}
      role="columnheader"
      aria-sort={sort === 1 ? 'ascending' : sort === -1 ? 'descending' : 'none'}
    >
      <button
        type="button"
        className="flex min-w-0 flex-1 items-center gap-1 px-2 py-1.5 text-left hover:bg-surface"
        onClick={onSort}
        title={t('csv.sort')}
      >
        <span className="truncate">{label}</span>
        {sort === 1 ? <ArrowUp aria-hidden="true" className="size-3 shrink-0" /> : null}
        {sort === -1 ? <ArrowDown aria-hidden="true" className="size-3 shrink-0" /> : null}
      </button>
      <span
        className="absolute top-0 right-0 h-full w-1.5 cursor-col-resize hover:bg-accent"
        aria-hidden="true"
        onPointerDown={(e) => {
          e.preventDefault()
          ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
          start.current = { x: e.clientX, w: width }
        }}
        onPointerMove={(e) => {
          if (start.current)
            onResize(Math.max(48, Math.min(800, start.current.w + e.clientX - start.current.x)))
        }}
        onPointerUp={() => {
          start.current = null
        }}
      />
    </span>
  )
}
