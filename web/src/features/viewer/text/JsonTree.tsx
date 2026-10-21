import type { Virtualizer } from '@tanstack/react-virtual'
import { useQuery } from '@tanstack/react-query'
import {
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  Copy,
  Search,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import type { Item } from '@/features/browser/api'
import { cn } from '@/lib/utils'
import { CodeViewer } from './CodeViewer'
import {
  allContainers,
  ancestors,
  displayPath,
  expandToDepth,
  flatten,
  searchKeys,
  type JsonRow,
  type JsonValue,
} from './jsonTree'
import { BinaryFileError, loadText } from './source'
import { TextState } from './TextState'
import { VirtualRows } from './VirtualRows'

/** Tree view up to this size (SPEC §8.14: ≤ 5 MB; bigger → text viewer). */
export const JSON_TREE_MAX = 5 * 1024 * 1024
const ROW = 22

/** JSON viewer (SPEC §8.14 JsonViewer): collapsible tree, key search with
 *  next/previous, copy path. Falls back to the text viewer for files over
 *  5 MB or invalid JSON. */
export function JsonViewer({ item }: { item: Item }) {
  const { t } = useTranslation('viewer')
  const big = item.size > JSON_TREE_MAX
  const src = useQuery({
    queryKey: ['viewer-text', item.path, item.etag],
    queryFn: ({ signal }) => loadText(item, signal),
    enabled: !big,
    staleTime: 30_000,
    retry: false,
  })
  const parsed = useMemo(() => {
    if (!src.data) return null
    try {
      return { ok: true as const, value: JSON.parse(src.data.text) as JsonValue }
    } catch (e) {
      return { ok: false as const, error: (e as Error).message }
    }
  }, [src.data])
  if (big) return <CodeViewer item={item} note={t('json.tooBig')} />
  if (src.isPending) return <TextState kind="loading" />
  if (src.isError)
    return <TextState kind={src.error instanceof BinaryFileError ? 'binary' : 'error'} />
  if (!parsed?.ok)
    return <CodeViewer item={item} note={t('json.invalid', { error: parsed?.error ?? '' })} />
  return <JsonTree value={parsed.value} />
}

export function JsonTree({ value }: { value: JsonValue }) {
  const { t } = useTranslation('viewer')
  const [expanded, setExpanded] = useState<Set<string>>(() => expandToDepth(value, 2))
  const [query, setQuery] = useState('')
  const [hit, setHit] = useState(0)
  const virt = useRef<Virtualizer<HTMLDivElement, Element> | null>(null)
  const rows = useMemo(() => flatten(value, expanded), [value, expanded])
  const matches = useMemo(() => searchKeys(value, query), [value, query])
  const matchSet = useMemo(() => new Set(matches), [matches])
  const current = matches.length ? matches[Math.min(hit, matches.length - 1)] : undefined

  const reveal = useCallback(
    (ptr: string) => {
      setExpanded((prev) => {
        const next = new Set(prev)
        for (const a of ancestors(ptr)) next.add(a)
        // scroll once the rows include it
        queueMicrotask(() => {
          const idx = flatten(value, next).findIndex((r) => r.ptr === ptr)
          if (idx >= 0)
            requestAnimationFrame(() => virt.current?.scrollToIndex(idx, { align: 'center' }))
        })
        return next
      })
    },
    [value],
  )
  // typing reveals the first match
  useEffect(() => {
    if (matches[0]) reveal(matches[0])
  }, [matches, reveal])
  const go = (d: 1 | -1) => {
    if (!matches.length) return
    const n = (hit + d + matches.length) % matches.length
    setHit(n)
    reveal(matches[n]!)
  }
  const toggle = (ptr: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(ptr)) next.delete(ptr)
      else next.add(ptr)
      return next
    })
  const copy = (ptr: string) => {
    void navigator.clipboard?.writeText(displayPath(ptr)).then(
      () => toast.success(t('json.copied', { path: displayPath(ptr) })),
      () => {},
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="json-tree">
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-border bg-surface px-2">
        <div className="relative w-64 max-w-[50%]">
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-text-muted"
          />
          <Input
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setHit(0)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                go(e.shiftKey ? -1 : 1)
              }
            }}
            placeholder={t('json.search')}
            aria-label={t('json.search')}
            className="h-8 pl-7 text-xs"
            data-testid="json-search"
          />
        </div>
        <span
          className="min-w-16 text-xs text-text-muted tabular"
          aria-live="polite"
          data-testid="json-matches"
        >
          {query
            ? matches.length
              ? t('json.matches', {
                  current: Math.min(hit, matches.length - 1) + 1,
                  total: matches.length,
                })
              : t('json.noMatches')
            : ''}
        </span>
        <IconButton label={t('json.prev')} onClick={() => go(-1)} disabled={!matches.length}>
          <ChevronDown aria-hidden="true" className="rotate-180" />
        </IconButton>
        <IconButton label={t('json.next')} onClick={() => go(1)} disabled={!matches.length}>
          <ChevronDown aria-hidden="true" />
        </IconButton>
        <span className="flex-1" />
        <IconButton
          label={t('json.expandAll')}
          onClick={() => setExpanded(allContainers(value))}
          data-testid="json-expand-all"
        >
          <ChevronsUpDown aria-hidden="true" />
        </IconButton>
        <IconButton
          label={t('json.collapseAll')}
          onClick={() => setExpanded(new Set())}
          data-testid="json-collapse-all"
        >
          <ChevronsDownUp aria-hidden="true" />
        </IconButton>
      </div>
      <VirtualTree
        rows={rows}
        expanded={expanded}
        matchSet={matchSet}
        current={current}
        onToggle={toggle}
        onCopy={copy}
        onReady={(v) => {
          virt.current = v
        }}
      />
    </div>
  )
}

function VirtualTree({
  rows,
  expanded,
  matchSet,
  current,
  onToggle,
  onCopy,
  onReady,
}: {
  rows: JsonRow[]
  expanded: ReadonlySet<string>
  matchSet: ReadonlySet<string>
  current: string | undefined
  onToggle: (ptr: string) => void
  onCopy: (ptr: string) => void
  onReady: (v: Virtualizer<HTMLDivElement, Element>) => void
}) {
  const { t } = useTranslation('viewer')
  return (
    <VirtualRows
      count={rows.length}
      rowHeight={ROW}
      onReady={onReady}
      testId="json-rows"
      role="tree"
      label={t('json.title')}
      className="bg-bg font-mono text-[13px]"
      renderRow={(i) => {
        const r = rows[i]!
        const open = expanded.has(r.ptr)
        return (
          <div
            role="treeitem"
            aria-level={r.depth + 1}
            aria-expanded={r.container ? open : undefined}
            className={cn(
              'group flex h-full items-center gap-1 pr-2 whitespace-nowrap hover:bg-surface-2',
              matchSet.has(r.ptr) && 'bg-accent-subtle',
              current === r.ptr && 'outline-2 -outline-offset-2 outline-accent',
            )}
            style={{ paddingLeft: 8 + r.depth * 16 }}
            data-ptr={r.ptr}
            data-testid="json-row"
          >
            {r.container ? (
              <button
                type="button"
                className="flex size-4 shrink-0 items-center justify-center text-text-muted hover:text-text"
                onClick={() => onToggle(r.ptr)}
                aria-label={open ? t('json.collapse') : t('json.expand')}
                data-testid="json-toggle"
              >
                {open ? (
                  <ChevronDown aria-hidden="true" className="size-3.5" />
                ) : (
                  <ChevronRight aria-hidden="true" className="size-3.5" />
                )}
              </button>
            ) : (
              <span className="size-4 shrink-0" />
            )}
            {r.key !== null ? (
              <span
                className={
                  typeof r.key === 'number'
                    ? 'text-text-muted'
                    : 'text-[color:var(--color-icon-code)]'
                }
              >
                {typeof r.key === 'number' ? r.key : JSON.stringify(r.key)}
                <span className="text-text-muted">:</span>
              </span>
            ) : null}
            <Value row={r} open={open} />
            <button
              type="button"
              className="ml-2 hidden text-text-muted group-hover:inline-flex hover:text-text focus-visible:inline-flex"
              onClick={() => onCopy(r.ptr)}
              aria-label={t('json.copyPath')}
              title={t('json.copyPath')}
            >
              <Copy aria-hidden="true" className="size-3.5" />
            </button>
          </div>
        )
      }}
    />
  )
}

function Value({ row, open }: { row: JsonRow; open: boolean }) {
  const v = row.value
  if (row.container) {
    const arr = Array.isArray(v)
    return (
      <span className="text-text-muted">
        {arr ? '[' : '{'}
        {open ? '' : ` ${row.size} `}
        {open ? '' : arr ? ']' : '}'}
      </span>
    )
  }
  if (v === null) return <span className="text-text-muted italic">{JSON.stringify(v)}</span>
  if (typeof v === 'string')
    return (
      <span
        className="truncate text-[color:var(--color-success)]"
        title={v.length > 80 ? v.slice(0, 2000) : undefined}
      >
        {JSON.stringify(v.length > 2000 ? `${v.slice(0, 2000)}…` : v)}
      </span>
    )
  if (typeof v === 'number')
    return <span className="text-[color:var(--color-accent)]">{String(v)}</span>
  return <span className="text-[color:var(--color-warning)]">{String(v)}</span>
}
