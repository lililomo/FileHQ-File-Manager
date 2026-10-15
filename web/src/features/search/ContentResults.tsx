import { useNavigate } from '@tanstack/react-router'
import { ChevronDown, ChevronRight, FolderOpen } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { Button } from '@/components/ui/button'
import { locationOf, useOpenLocation } from '@/features/browser/openLocation'
import { cn } from '@/lib/utils'
import type { ContentMatch } from './api'
import type { FileGroup } from './grouping'

/** Highlights the matched spans of a line: every match of the same line
 *  arrives as its own row (col + len in runes). */
export function Highlight({
  text,
  spans,
}: {
  text: string
  spans: { col: number; len: number }[]
}) {
  const parts: { s: string; hit: boolean }[] = []
  const chars = [...text]
  let last = 0
  for (const sp of [...spans].sort((a, b) => a.col - b.col)) {
    const start = sp.col - 1
    if (start < last || start >= chars.length) continue
    if (start > last) parts.push({ s: chars.slice(last, start).join(''), hit: false })
    const end = Math.min(chars.length, start + Math.max(1, sp.len))
    parts.push({ s: chars.slice(start, end).join(''), hit: true })
    last = end
  }
  if (last < chars.length) parts.push({ s: chars.slice(last).join(''), hit: false })
  return (
    <>
      {parts.map((p, i) =>
        p.hit ? (
          <mark key={i} className="rounded-sm bg-accent/30 text-text">
            {p.s}
          </mark>
        ) : (
          <span key={i}>{p.s}</span>
        ),
      )}
    </>
  )
}

/** Content results grouped per file; each line opens the editor at that
 *  line (`/editor?path=&line=`, Step 16a). */
export function ContentResults({
  groups,
  queryLength,
}: {
  groups: FileGroup[]
  queryLength: number
}) {
  const { t } = useTranslation(['search'])
  const navigate = useNavigate()
  const openLocation = useOpenLocation()
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const lines = useMemo(
    () =>
      groups.map((g) => {
        const byLine = new Map<number, { m: ContentMatch; spans: { col: number; len: number }[] }>()
        for (const m of g.matches) {
          const span = { col: m.col, len: m.len ?? queryLength }
          const row = byLine.get(m.line)
          if (row) row.spans.push(span)
          else byLine.set(m.line, { m, spans: [span] })
        }
        return { file: g.file, rows: [...byLine.values()], count: g.matches.length }
      }),
    [groups, queryLength],
  )
  const openLine = (file: string, line: number) =>
    void navigate({ to: '/editor', search: { path: file, line } })
  return (
    <div className="min-h-0 flex-1 overflow-auto" data-testid="search-content-body">
      {lines.map((g) => {
        const open = !collapsed[g.file]
        return (
          <section
            key={g.file}
            className="border-b border-border"
            data-testid="search-content-file"
            data-path={g.file}
          >
            <header className="sticky top-0 z-10 flex items-center gap-2 bg-surface-2 px-3 py-1.5 text-sm">
              <button
                type="button"
                className="inline-flex items-center gap-1"
                onClick={() => setCollapsed((c) => ({ ...c, [g.file]: open }))}
                aria-expanded={open}
                aria-label={open ? t('search:results.collapse') : t('search:results.expand')}
              >
                {open ? (
                  <ChevronDown className="size-4" aria-hidden="true" />
                ) : (
                  <ChevronRight className="size-4" aria-hidden="true" />
                )}
              </button>
              <FileIcon category="text" size={16} />
              <span className="truncate font-medium" title={g.file}>
                {g.file.slice(g.file.lastIndexOf('/') + 1)}
              </span>
              <span className="hidden truncate font-mono text-xs text-text-muted sm:block">
                {locationOf(g.file)}
              </span>
              <span className="ml-auto rounded-full bg-surface px-2 text-xs tabular text-text-muted">
                {g.count}
              </span>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={t('search:results.openLocation')}
                onClick={() => openLocation({ path: g.file, isDir: false })}
              >
                <FolderOpen aria-hidden="true" />
              </Button>
            </header>
            {open
              ? g.rows.map(({ m, spans }) => (
                  <button
                    key={m.line}
                    type="button"
                    className={cn(
                      'flex w-full items-start gap-3 px-3 py-0.5 text-left font-mono text-xs hover:bg-surface-2',
                    )}
                    onClick={() => openLine(g.file, m.line)}
                    title={t('search:results.openLine', { line: m.line })}
                    data-testid="search-content-line"
                    data-line={m.line}
                  >
                    <span className="w-10 shrink-0 text-right tabular text-text-muted">
                      {m.line}
                    </span>
                    <span className="min-w-0 flex-1 truncate whitespace-pre">
                      <Highlight text={m.text} spans={spans} />
                    </span>
                  </button>
                ))
              : null}
          </section>
        )
      })}
    </div>
  )
}
