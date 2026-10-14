import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown, ArrowUp, Lock } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Checkbox } from '@/components/ui/checkbox'
import { formatBytes, formatDateTimeFull } from '@/lib/format'
import { cn } from '@/lib/utils'
import { entryCategory, type ArchiveEntry } from './api'
import { ratioOf, type EntrySort, type EntrySortField } from './entrySort'

export interface EntryTableProps {
  items: ArchiveEntry[]
  selected: Set<string>
  active: string | null
  sort: EntrySort
  onSort: (s: EntrySort) => void
  onSelect: (next: Set<string>, active: string | null) => void
  /** Enter / double-click: folder → navigate, file → preview. */
  onActivate: (entry: ArchiveEntry) => void
  /** Backspace: up one level. */
  onUp: () => void
  onDelete?: ((entries: ArchiveEntry[]) => void) | undefined
  /** Right-click selects the row under the pointer unless it is already selected. */
  onContextRow?: ((entry: ArchiveEntry) => void) | undefined
  className?: string
}

const ROW = 32

const COLUMNS: { id: EntrySortField; className: string; align?: 'right' }[] = [
  { id: 'name', className: 'min-w-0 flex-1' },
  { id: 'size', className: 'w-24 shrink-0', align: 'right' },
  { id: 'compressed', className: 'hidden w-24 shrink-0 md:block', align: 'right' },
  { id: 'ratio', className: 'hidden w-16 shrink-0 lg:block', align: 'right' },
  { id: 'mtime', className: 'hidden w-44 shrink-0 lg:block' },
]

/** Virtualised member grid of the archive browser (SPEC §8.11): roving
 *  focus through aria-activedescendant, Shift/Ctrl selection, Space toggle,
 *  Enter opens, Backspace goes up, Ctrl+A selects the page, Delete removes. */
export function EntryTable({
  items,
  selected,
  active,
  sort,
  onSort,
  onSelect,
  onActivate,
  onUp,
  onDelete,
  onContextRow,
  className,
}: EntryTableProps) {
  const { t, i18n } = useTranslation(['archive'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const scrollRef = useRef<HTMLDivElement>(null)
  const gridRef = useRef<HTMLDivElement>(null)
  const id = useRef(`arc-${Math.random().toString(36).slice(2, 8)}`).current
  const [anchor, setAnchor] = useState<string | null>(null)
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW,
    overscan: 10,
  })
  const indexOf = useMemo(() => new Map(items.map((e, i) => [e.path, i])), [items])
  const activeIndex = active ? (indexOf.get(active) ?? -1) : -1

  useEffect(() => {
    if (activeIndex >= 0) virtualizer.scrollToIndex(activeIndex, { align: 'auto' })
  }, [activeIndex, virtualizer])

  const selectRange = useCallback(
    (from: string | null, to: string, additive: boolean) => {
      const a = from ? (indexOf.get(from) ?? -1) : -1
      const b = indexOf.get(to) ?? -1
      if (a < 0 || b < 0) {
        onSelect(new Set([to]), to)
        return
      }
      const next = additive ? new Set(selected) : new Set<string>()
      for (let i = Math.min(a, b); i <= Math.max(a, b); i++) next.add(items[i]!.path)
      onSelect(next, to)
    },
    [indexOf, items, onSelect, selected],
  )

  const click = (e: React.MouseEvent, entry: ArchiveEntry) => {
    if (e.shiftKey) {
      selectRange(anchor ?? active, entry.path, e.ctrlKey || e.metaKey)
      return
    }
    if (e.ctrlKey || e.metaKey) {
      const next = new Set(selected)
      if (next.has(entry.path)) next.delete(entry.path)
      else next.add(entry.path)
      onSelect(next, entry.path)
      setAnchor(entry.path)
      return
    }
    onSelect(new Set([entry.path]), entry.path)
    setAnchor(entry.path)
  }

  const move = (e: React.KeyboardEvent, nextIndex: number) => {
    const clamped = Math.max(0, Math.min(items.length - 1, nextIndex))
    const entry = items[clamped]
    if (!entry) return
    if (e.shiftKey) selectRange(anchor ?? active, entry.path, false)
    else {
      onSelect(new Set([entry.path]), entry.path)
      setAnchor(entry.path)
    }
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (items.length === 0) return
    const cur = activeIndex < 0 ? 0 : activeIndex
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        move(e, activeIndex < 0 ? 0 : cur + 1)
        break
      case 'ArrowUp':
        e.preventDefault()
        move(e, activeIndex < 0 ? 0 : cur - 1)
        break
      case 'Home':
        e.preventDefault()
        move(e, 0)
        break
      case 'End':
        e.preventDefault()
        move(e, items.length - 1)
        break
      case 'PageDown':
        e.preventDefault()
        move(e, cur + 10)
        break
      case 'PageUp':
        e.preventDefault()
        move(e, cur - 10)
        break
      case ' ': {
        e.preventDefault()
        const entry = items[cur]
        if (!entry) return
        const next = new Set(selected)
        if (next.has(entry.path)) next.delete(entry.path)
        else next.add(entry.path)
        onSelect(next, entry.path)
        break
      }
      case 'Enter': {
        e.preventDefault()
        const entry = active ? items[activeIndex] : items[0]
        if (entry) onActivate(entry)
        break
      }
      case 'Backspace':
        e.preventDefault()
        onUp()
        break
      case 'Delete':
        if (onDelete && selected.size) {
          e.preventDefault()
          onDelete(items.filter((it) => selected.has(it.path)))
        }
        break
      case 'Escape':
        if (selected.size) {
          e.preventDefault()
          e.stopPropagation()
          onSelect(new Set(), active)
        }
        break
      case 'a':
      case 'A':
        if (e.ctrlKey || e.metaKey) {
          e.preventDefault()
          onSelect(new Set(items.map((it) => it.path)), active)
        }
        break
      default:
        break
    }
  }

  const allSelected = items.length > 0 && items.every((it) => selected.has(it.path))
  const someSelected = !allSelected && items.some((it) => selected.has(it.path))
  const headerSort = (field: EntrySortField) =>
    onSort(
      sort.field === field
        ? { field, dir: sort.dir === 'asc' ? 'desc' : 'asc' }
        : { field, dir: field === 'name' ? 'asc' : 'desc' },
    )

  return (
    <div ref={scrollRef} className={cn('min-h-0 flex-1 overflow-auto', className)}>
      <div
        ref={gridRef}
        role="grid"
        id={id}
        aria-label={t('archive:browser.grid')}
        aria-multiselectable="true"
        aria-rowcount={items.length + 1}
        aria-activedescendant={active ? `${id}-${activeIndex}` : undefined}
        tabIndex={0}
        onKeyDown={onKeyDown}
        className="min-w-full text-sm outline-none focus-visible:ring-2 focus-visible:ring-accent"
        data-testid="archive-grid"
      >
        <div
          role="row"
          className="sticky top-0 z-10 flex h-8 items-center gap-2 border-b border-border bg-surface px-3 text-xs text-text-muted"
        >
          <div role="columnheader" className="w-6 shrink-0">
            <Checkbox
              checked={allSelected ? true : someSelected ? 'indeterminate' : false}
              onCheckedChange={(v) =>
                onSelect(v ? new Set(items.map((it) => it.path)) : new Set(), active)
              }
              aria-label={t('archive:browser.selectAll')}
              data-testid="archive-select-all"
            />
          </div>
          {COLUMNS.map((c) => (
            <div
              key={c.id}
              role="columnheader"
              className={c.className}
              aria-sort={
                sort.field === c.id ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'
              }
            >
              <button
                type="button"
                className={cn(
                  'inline-flex items-center gap-1 hover:text-text',
                  c.align === 'right' && 'w-full justify-end',
                )}
                onClick={() => headerSort(c.id)}
                data-testid={`archive-col-${c.id}`}
              >
                {t(`archive:browser.columns.${c.id}`)}
                {sort.field === c.id ? (
                  sort.dir === 'asc' ? (
                    <ArrowUp className="size-3" aria-hidden="true" />
                  ) : (
                    <ArrowDown className="size-3" aria-hidden="true" />
                  )
                ) : null}
              </button>
            </div>
          ))}
        </div>
        <div
          role="rowgroup"
          style={{ height: virtualizer.getTotalSize(), position: 'relative' }}
          data-testid="archive-body"
        >
          {virtualizer.getVirtualItems().map((v) => {
            const it = items[v.index]!
            const isSel = selected.has(it.path)
            const ratio = ratioOf(it)
            return (
              <div
                key={it.path}
                id={`${id}-${v.index}`}
                role="row"
                aria-selected={isSel}
                aria-rowindex={v.index + 2}
                data-path={it.path}
                data-name={it.name}
                data-dir={it.isDir ? 'true' : 'false'}
                data-testid="archive-row"
                style={{ position: 'absolute', top: v.start, height: v.size, left: 0, right: 0 }}
                className={cn(
                  'flex items-center gap-2 border-b border-border/60 px-3',
                  isSel ? 'bg-accent-subtle' : 'hover:bg-surface-2',
                  active === it.path && 'outline outline-1 -outline-offset-1 outline-accent',
                )}
                onClick={(e) => click(e, it)}
                onDoubleClick={() => onActivate(it)}
                onContextMenu={() => {
                  if (!isSel) {
                    onSelect(new Set([it.path]), it.path)
                    setAnchor(it.path)
                  }
                  onContextRow?.(it)
                }}
              >
                <div role="gridcell" className="w-6 shrink-0" onClick={(e) => e.stopPropagation()}>
                  <Checkbox
                    checked={isSel}
                    onCheckedChange={() => {
                      const next = new Set(selected)
                      if (next.has(it.path)) next.delete(it.path)
                      else next.add(it.path)
                      onSelect(next, it.path)
                    }}
                    aria-label={t('archive:browser.selectRow', { name: it.name })}
                  />
                </div>
                <div role="gridcell" className="flex min-w-0 flex-1 items-center gap-2">
                  <FileIcon
                    category={toIconCategory(entryCategory(it))}
                    size={18}
                    className="shrink-0"
                  />
                  <span className="truncate" title={it.path}>
                    {it.name}
                  </span>
                  {it.encrypted ? (
                    <Lock
                      className="size-3 shrink-0 text-text-muted"
                      aria-label={t('archive:browser.encrypted')}
                    />
                  ) : null}
                </div>
                <div role="gridcell" className="w-24 shrink-0 text-right tabular">
                  {it.isDir
                    ? t('archive:browser.items', { count: it.itemCount })
                    : formatBytes(it.size, lang)}
                </div>
                <div
                  role="gridcell"
                  className="hidden w-24 shrink-0 text-right tabular text-text-muted md:block"
                >
                  {it.isDir || it.compressedSize < 0 ? '—' : formatBytes(it.compressedSize, lang)}
                </div>
                <div
                  role="gridcell"
                  className="hidden w-16 shrink-0 text-right tabular text-text-muted lg:block"
                >
                  {ratio === null ? '—' : `${ratio} %`}
                </div>
                <div
                  role="gridcell"
                  className="hidden w-44 shrink-0 truncate text-xs text-text-muted lg:block"
                >
                  {it.mtime.startsWith('0001') ? '—' : formatDateTimeFull(it.mtime, lang)}
                </div>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
