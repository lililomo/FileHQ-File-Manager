/** Streaming CSV/TSV parser for the table viewer (SPEC §8.14 TableViewer,
 *  PROMPT 17b task 4). RFC 4180 quoting ("" escapes, separators and line
 *  breaks inside quotes), CRLF/LF/CR, chunks of any size — rows are handed
 *  out as soon as they are complete so a 100k-row file renders while it
 *  downloads. No dependency: the state machine is ~60 lines. */

export const CSV_DELIMITERS = [',', ';', '\t', '|'] as const

/** Pick the delimiter that splits the sample's first lines most
 *  consistently (quotes respected); `,` when nothing fits. */
export function detectDelimiter(sample: string): string {
  const lines: string[] = []
  let inQ = false
  let cur = ''
  for (let i = 0; i < sample.length && lines.length < 20; i++) {
    const ch = sample[i]!
    if (ch === '"') inQ = !inQ
    if (!inQ && (ch === '\n' || ch === '\r')) {
      if (cur) lines.push(cur)
      cur = ''
      continue
    }
    cur += ch
  }
  if (cur && lines.length < 20 && sample.length < 64 * 1024) lines.push(cur)
  let best = ','
  let bestScore = 0
  for (const d of CSV_DELIMITERS) {
    const counts = lines.map((l) => countOutsideQuotes(l, d))
    if (!counts.length || counts[0] === 0) continue
    const same = counts.filter((c) => c === counts[0]).length
    const score = same * 1000 + counts[0]!
    if (score > bestScore) {
      bestScore = score
      best = d
    }
  }
  return best
}

function countOutsideQuotes(line: string, d: string): number {
  let n = 0
  let inQ = false
  for (const ch of line) {
    if (ch === '"') inQ = !inQ
    else if (!inQ && ch === d) n++
  }
  return n
}

/** Incremental parser: `push` text chunks, collect completed rows. */
export class CsvParser {
  private row: string[] = []
  private field = ''
  private inQuotes = false
  /** a quote inside a quoted field: either "" or the closing quote */
  private quotePending = false
  private lastWasCR = false
  private any = false

  constructor(private readonly delimiter: string) {}

  push(chunk: string): string[][] {
    const out: string[][] = []
    const d = this.delimiter
    for (let i = 0; i < chunk.length; i++) {
      const ch = chunk[i]!
      if (this.lastWasCR) {
        this.lastWasCR = false
        if (ch === '\n') continue
      }
      if (this.inQuotes) {
        if (this.quotePending) {
          this.quotePending = false
          if (ch === '"') {
            this.field += '"'
            continue
          }
          this.inQuotes = false // closing quote; fall through to normal handling
        } else if (ch === '"') {
          this.quotePending = true
          continue
        } else {
          this.field += ch
          continue
        }
      }
      if (ch === '"' && this.field === '') {
        this.inQuotes = true
        this.any = true
      } else if (ch === d) {
        this.row.push(this.field)
        this.field = ''
        this.any = true
      } else if (ch === '\n' || ch === '\r') {
        this.lastWasCR = ch === '\r'
        if (this.any || this.field !== '' || this.row.length) {
          this.row.push(this.field)
          out.push(this.row)
        }
        this.row = []
        this.field = ''
        this.any = false
      } else {
        this.field += ch
        this.any = true
      }
    }
    return out
  }

  /** Flush the last row (file without a trailing newline). */
  end(): string[][] {
    if (this.quotePending) {
      this.quotePending = false
      this.inQuotes = false
    }
    if (!this.any && this.field === '' && this.row.length === 0) return []
    this.row.push(this.field)
    const last = this.row
    this.row = []
    this.field = ''
    this.any = false
    return [last]
  }
}

/** Convenience for tests and small strings. */
export function parseCsv(text: string, delimiter = detectDelimiter(text)): string[][] {
  const p = new CsvParser(delimiter)
  return [...p.push(text), ...p.end()]
}

export interface StreamResult {
  rows: string[][]
  delimiter: string
  /** stopped at `maxRows` (or the byte cap) */
  truncated: boolean
}

/** Parse a byte stream incrementally; `onProgress` sees the growing row
 *  array after each chunk. Stops (and cancels the stream) after `maxRows`. */
export async function parseCsvStream(
  stream: ReadableStream<Uint8Array>,
  opts: {
    maxRows: number
    delimiter?: string
    onProgress?: (rows: string[][]) => void
    signal?: AbortSignal
  },
): Promise<StreamResult> {
  const reader = stream.getReader()
  const decoder = new TextDecoder('utf-8')
  const rows: string[][] = []
  let parser: CsvParser | null = null
  let delimiter = opts.delimiter ?? ''
  let pending = ''
  let truncated = false
  const take = (got: string[][]) => {
    for (const r of got) {
      if (rows.length >= opts.maxRows) {
        truncated = true
        return
      }
      rows.push(r)
    }
  }
  try {
    for (;;) {
      if (opts.signal?.aborted) throw new DOMException('aborted', 'AbortError')
      const { done, value } = await reader.read()
      let text = done ? decoder.decode() : decoder.decode(value, { stream: true })
      if (!parser) {
        pending += text
        // wait for a few lines (or the end) before guessing the delimiter
        if (!done && pending.length < 16 * 1024 && !/\n[^\n]*\n[^\n]*\n/.test(pending)) continue
        text = pending.replace(/^\uFEFF/, '')
        delimiter ||= detectDelimiter(text.slice(0, 64 * 1024))
        parser = new CsvParser(delimiter)
      }
      take(parser.push(text))
      if (done) {
        take(parser.end())
        break
      }
      opts.onProgress?.(rows)
      if (truncated) break
    }
  } finally {
    if (truncated) await reader.cancel().catch(() => {})
    else reader.releaseLock()
  }
  return { rows, delimiter: delimiter || ',', truncated }
}
