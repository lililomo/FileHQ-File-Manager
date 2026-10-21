import { History, Replace, Search as SearchIcon, SlidersHorizontal, Trash2, X } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ApiError } from '@/lib/api'
import { formatNumber, formatRelativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import {
  clearSearchHistory,
  getSearchHistory,
  searchContent,
  searchNames,
  type ContentMatch,
  type ContentSummary,
  type HistoryEntry,
  type NameItem,
  type SearchKind,
} from './api'
import { ContentResults } from './ContentResults'
import { groupMatches } from './grouping'
import { Filters } from './Filters'
import { DEFAULT_FILTERS, isDefaultFilters, type FilterState } from './filters'
import { toContentQuery, toNameFilters } from './query'
import { NameResults } from './NameResults'
import { ReplacePanel } from './ReplacePanel'

export interface SearchParams {
  q?: string | undefined
  path?: string | undefined
  kind?: SearchKind | undefined
}

interface NameState {
  items: NameItem[]
  total: number
  truncated: boolean
  tookMs: number
  nextCursor?: string | undefined
}

/** `/search` (SPEC §8.12, §9.3): name and content search with filters,
 *  history and replace in files. The query, folder and tab live in the URL
 *  so "Cari di folder ini" / Ctrl+Shift+F / the top bar can deep-link. */
export function SearchPage({ params }: { params: SearchParams }) {
  const { t, i18n } = useTranslation(['search', 'files'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const [kind, setKind] = useState<SearchKind>(params.kind ?? 'name')
  const [q, setQ] = useState(params.q ?? '')
  const [path, setPath] = useState(params.path ?? '/')
  const [filters, setFilters] = useState<FilterState>({ ...DEFAULT_FILTERS })
  const [showFilters, setShowFilters] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [names, setNames] = useState<NameState | null>(null)
  const [matches, setMatches] = useState<ContentMatch[]>([])
  const [summary, setSummary] = useState<ContentSummary | null>(null)
  const [ran, setRan] = useState<{
    kind: SearchKind
    q: string
    path: string
    filters: FilterState
  } | null>(null)
  const [replaceOpen, setReplaceOpen] = useState(false)
  const [history, setHistory] = useState<HistoryEntry[] | null>(null)
  const [historyOpen, setHistoryOpen] = useState(false)
  const abort = useRef<AbortController | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const describeError = useCallback(
    (e: unknown): string => {
      if (e instanceof ApiError) {
        const field = (e.details.fields as Record<string, string> | undefined)?.q
        if (field === 'invalid_regex' || field === 'invalid_glob')
          return t('search:errors.invalid_regex')
        if (e.code === 'RATE_LIMITED')
          return t('search:errors.RATE_LIMITED', { seconds: e.details.retryAfter ?? 60 })
        const specific = t(`search:errors.${e.code}`, { defaultValue: '' })
        if (specific) return specific
        return t('search:errors.generic', { message: e.message })
      }
      return t('search:errors.generic', { message: e instanceof Error ? e.message : String(e) })
    },
    [t],
  )

  const stop = useCallback(() => {
    abort.current?.abort()
    abort.current = null
    setBusy(false)
  }, [])

  const run = useCallback(
    async (k: SearchKind, query: string, scope: string, f: FilterState) => {
      abort.current?.abort()
      const ctl = new AbortController()
      abort.current = ctl
      setError(null)
      setBusy(true)
      setRan({ kind: k, q: query, path: scope, filters: f })
      try {
        if (k === 'name') {
          setNames(null)
          const res = await searchNames(query, scope, toNameFilters(f), undefined, ctl.signal)
          if (ctl.signal.aborted) return
          setNames({
            items: res.items,
            total: res.total,
            truncated: res.truncated,
            tookMs: res.tookMs,
            nextCursor: res.nextCursor,
          })
        } else {
          setMatches([])
          setSummary(null)
          const buffer: ContentMatch[] = []
          let flushTimer: ReturnType<typeof setTimeout> | null = null
          const flush = () => {
            flushTimer = null
            if (buffer.length) {
              const chunk = buffer.splice(0, buffer.length)
              setMatches((m) => m.concat(chunk))
            }
          }
          const sum = await searchContent(
            toContentQuery(query, scope, f),
            (m) => {
              buffer.push(m)
              if (!flushTimer) flushTimer = setTimeout(flush, 80)
            },
            ctl.signal,
          )
          if (flushTimer) clearTimeout(flushTimer)
          flush()
          if (ctl.signal.aborted) return
          setSummary(sum)
        }
        setHistory(null)
      } catch (e) {
        if ((e as { name?: string }).name === 'AbortError') return
        setError(describeError(e))
      } finally {
        if (abort.current === ctl) {
          abort.current = null
          setBusy(false)
        }
      }
    },
    [describeError],
  )

  const submit = () => {
    const query = q.trim()
    if (!query && (kind === 'content' || isDefaultFilters(filters))) {
      inputRef.current?.focus()
      return
    }
    void run(kind, query, path.trim() || '/', filters)
  }

  // deep link: /search?q=…&path=…&kind=… runs once per mount (the route
  // remounts the page when the URL changes from outside: top bar, "Cari di
  // folder ini", Ctrl+Shift+F, palette); deferred so the effect itself
  // does not set state synchronously
  useEffect(() => {
    const id = setTimeout(() => {
      if (params.q)
        void run(params.kind ?? 'name', params.q, params.path ?? '/', { ...DEFAULT_FILTERS })
      else inputRef.current?.focus()
    }, 0)
    return () => clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- mount only; the route key remounts on param change
  }, [])
  useEffect(() => () => abort.current?.abort(), [])

  const loadMore = async () => {
    if (!names?.nextCursor || !ran) return
    setBusy(true)
    try {
      const res = await searchNames(ran.q, ran.path, toNameFilters(ran.filters), names.nextCursor)
      setNames((n) =>
        n ? { ...n, items: n.items.concat(res.items), nextCursor: res.nextCursor } : n,
      )
    } catch (e) {
      toast.error(describeError(e))
    } finally {
      setBusy(false)
    }
  }

  const openHistory = async (open: boolean) => {
    setHistoryOpen(open)
    if (open && history === null) {
      try {
        const res = await getSearchHistory()
        setHistory(res.items)
      } catch {
        setHistory([])
      }
    }
  }
  const applyHistory = (h: HistoryEntry) => {
    setHistoryOpen(false)
    setKind(h.kind)
    setQ(h.query)
    setPath(h.path || '/')
    const f: FilterState = { ...DEFAULT_FILTERS }
    const hf = h.filters ?? {}
    if (hf.recursive === false) f.recursive = false
    if (hf.regex === true) f.regex = true
    if (hf.glob === true) f.glob = true
    if (hf.caseSensitive === true) f.caseSensitive = true
    if (hf.wholeWord === true) f.wholeWord = true
    if (Array.isArray(hf.ext)) f.ext = (hf.ext as string[]).join(', ')
    if (Array.isArray(hf.types) && hf.types.length) f.type = String(hf.types[0])
    if (typeof hf.tag === 'string') f.tag = hf.tag
    setFilters(f)
    void run(h.kind, h.query, h.path || '/', f)
  }

  const groups = useMemo(() => groupMatches(matches), [matches])
  const activeFilters = !isDefaultFilters(filters)
  const contentQuery =
    ran && ran.kind === 'content' ? toContentQuery(ran.q, ran.path, ran.filters) : null

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="search-page">
      <header className="border-b border-border px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-lg font-semibold">{t('search:title')}</h1>
          <Tabs value={kind} onValueChange={(v) => setKind(v as SearchKind)} className="ml-2">
            <TabsList>
              <TabsTrigger value="name" data-testid="search-tab-name">
                {t('search:tabs.name')}
              </TabsTrigger>
              <TabsTrigger value="content" data-testid="search-tab-content">
                {t('search:tabs.content')}
              </TabsTrigger>
            </TabsList>
          </Tabs>
          <Popover open={historyOpen} onOpenChange={(o) => void openHistory(o)}>
            <PopoverTrigger asChild>
              <Button size="sm" variant="ghost" className="ml-auto" data-testid="search-history">
                <History aria-hidden="true" />
                {t('search:history.label')}
              </Button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-96 p-0">
              <div className="flex items-center justify-between border-b border-border px-3 py-2 text-sm font-medium">
                {t('search:history.label')}
                <Button
                  size="xs"
                  variant="ghost"
                  disabled={!history?.length}
                  onClick={() => {
                    void clearSearchHistory().then(() => setHistory([]))
                  }}
                  data-testid="search-history-clear"
                >
                  <Trash2 aria-hidden="true" />
                  {t('search:history.clear')}
                </Button>
              </div>
              <ul className="max-h-80 overflow-auto py-1" data-testid="search-history-list">
                {history?.length ? (
                  history.map((h) => (
                    <li key={h.id}>
                      <button
                        type="button"
                        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm hover:bg-surface-2"
                        onClick={() => applyHistory(h)}
                      >
                        <span className="rounded bg-surface-2 px-1.5 text-[10px] uppercase text-text-muted">
                          {h.kind === 'name'
                            ? t('search:history.kindName')
                            : t('search:history.kindContent')}
                        </span>
                        <span className="truncate font-mono">{h.query}</span>
                        <span className="truncate text-xs text-text-muted">{h.path}</span>
                        <span className="ml-auto shrink-0 text-xs text-text-muted">
                          {formatRelativeTime(h.at, lang)}
                        </span>
                      </button>
                    </li>
                  ))
                ) : (
                  <li className="px-3 py-2 text-sm text-text-muted">{t('search:history.empty')}</li>
                )}
              </ul>
            </PopoverContent>
          </Popover>
        </div>
        <form
          className="mt-2 flex flex-wrap items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
          role="search"
        >
          <div className="relative min-w-0 flex-1 basis-64">
            <SearchIcon
              className="pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2 text-text-muted"
              aria-hidden="true"
            />
            <Input
              ref={inputRef}
              className={cn('h-9 pl-8', filters.regex && 'font-mono')}
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={
                kind === 'name'
                  ? t('search:query.placeholder')
                  : t('search:query.placeholderContent')
              }
              aria-label={t('search:title')}
              data-testid="search-input"
            />
          </div>
          <label className="flex items-center gap-1 text-xs text-text-muted">
            {t('search:scope.label')}
            <Input
              className="h-9 w-56 font-mono text-xs"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              aria-label={t('search:scope.label')}
              data-testid="search-path"
            />
          </label>
          <label className="flex items-center gap-1 text-xs">
            <Checkbox
              checked={filters.recursive}
              onCheckedChange={(c) => setFilters((f) => ({ ...f, recursive: c === true }))}
              data-testid="search-recursive"
            />
            {t('search:scope.recursive')}
          </label>
          <Button
            type="button"
            size="sm"
            variant={showFilters || activeFilters ? 'secondary' : 'outline'}
            onClick={() => setShowFilters((v) => !v)}
            aria-pressed={showFilters}
            data-testid="search-filters-toggle"
          >
            <SlidersHorizontal aria-hidden="true" />
            {t('search:filters.toggle')}
          </Button>
          {busy ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={stop}
              data-testid="search-stop"
            >
              <X aria-hidden="true" />
              {t('search:query.stop')}
            </Button>
          ) : (
            <Button type="submit" size="sm" data-testid="search-submit">
              {t('search:query.submit')}
            </Button>
          )}
          {kind === 'content' ? (
            <Button
              type="button"
              size="sm"
              variant={replaceOpen ? 'secondary' : 'outline'}
              onClick={() => setReplaceOpen((v) => !v)}
              aria-pressed={replaceOpen}
              data-testid="search-replace-toggle"
            >
              <Replace aria-hidden="true" />
              {t('search:replace.button')}
            </Button>
          ) : null}
        </form>
      </header>
      {showFilters ? <Filters kind={kind} value={filters} onChange={setFilters} /> : null}
      <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
        <div className="flex min-h-0 flex-1 flex-col">
          <div
            className="flex items-center gap-2 border-b border-border px-4 py-1.5 text-xs text-text-muted"
            aria-live="polite"
            data-testid="search-status"
          >
            {error ? (
              <span className="text-danger" role="alert">
                {error}
              </span>
            ) : busy ? (
              <span>{t('search:results.searching')}</span>
            ) : ran?.kind === 'name' && names ? (
              <>
                <span>
                  {t('search:results.countIn', {
                    n: formatNumber(names.total, lang),
                    ms: Math.max(1, names.tookMs), // sub-millisecond answers read as "0 ms" (review D)
                  })}
                </span>
                {names.truncated ? (
                  <span className="text-warning">
                    {t('search:results.truncated', { n: formatNumber(names.total, lang) })}
                  </span>
                ) : null}
              </>
            ) : ran?.kind === 'content' && summary ? (
              <>
                <span>
                  {t('search:results.files', {
                    files: summary.files,
                    matches: formatNumber(summary.matches, lang),
                  })}
                </span>
                <span>· {t('search:results.engine', { engine: summary.engine })}</span>
                {summary.truncated ? (
                  <span className="text-warning">
                    {t('search:results.truncatedContent', {
                      n: formatNumber(summary.matches, lang),
                    })}
                  </span>
                ) : null}
              </>
            ) : null}
          </div>
          {!ran ? (
            <EmptyState
              icon={SearchIcon}
              title={t('search:results.idle')}
              description={t('search:results.idleHint')}
              className="h-full"
            />
          ) : ran.kind === 'name' ? (
            names && names.items.length === 0 ? (
              <EmptyState
                icon={SearchIcon}
                title={t('search:results.none', { q: ran.q || '*' })}
                className="h-full"
              />
            ) : names ? (
              <NameResults
                items={names.items}
                hasMore={!!names.nextCursor}
                loadingMore={busy}
                onLoadMore={() => void loadMore()}
              />
            ) : null
          ) : matches.length === 0 && summary ? (
            <EmptyState
              icon={SearchIcon}
              title={t('search:results.none', { q: ran.q })}
              className="h-full"
            />
          ) : (
            <ContentResults
              groups={groups}
              queryLength={ran.filters.regex ? 1 : [...ran.q].length}
            />
          )}
        </div>
        {kind === 'content' && replaceOpen && contentQuery ? (
          <ReplacePanel
            query={contentQuery}
            onClose={() => setReplaceOpen(false)}
            onApplied={() => {
              if (ran) void run(ran.kind, ran.q, ran.path, ran.filters)
            }}
          />
        ) : null}
      </div>
    </div>
  )
}
