import type { ContentQuery, NameFilters } from './api'
import type { FilterState } from './filters'

const kb = (v: string): number | undefined => {
  const n = Number(v)
  return v && Number.isFinite(n) && n > 0 ? Math.round(n * 1024) : undefined
}

export function toNameFilters(f: FilterState): NameFilters {
  const out: NameFilters = {}
  if (!f.recursive) out.recursive = false
  if (f.glob) out.glob = true
  if (f.regex) out.regex = true
  if (f.type) out.types = [f.type]
  if (f.ext.trim()) out.ext = f.ext.trim()
  const min = kb(f.minKB)
  const max = kb(f.maxKB)
  if (min) out.minSize = min
  if (max) out.maxSize = max
  if (f.from) out.from = f.from
  if (f.to) out.to = f.to
  if (f.tag) out.tag = f.tag
  return out
}

export function toContentQuery(q: string, path: string, f: FilterState): ContentQuery {
  const out: ContentQuery = {
    q,
    path,
    recursive: f.recursive,
    regex: f.regex,
    caseSensitive: f.caseSensitive,
    wholeWord: f.wholeWord,
  }
  const ext = f.ext
    .split(',')
    .map((e) => e.trim().replace(/^\./, ''))
    .filter(Boolean)
  if (ext.length) out.ext = ext
  const min = kb(f.minKB)
  const max = kb(f.maxKB)
  if (min) out.minSize = min
  if (max) out.maxSize = max
  if (f.from) out.from = f.from
  if (f.to) out.to = f.to
  return out
}
