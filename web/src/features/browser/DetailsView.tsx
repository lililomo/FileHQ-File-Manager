import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown, ArrowUp } from 'lucide-react'
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'
import { Checkbox } from '@/components/ui/checkbox'
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from '@/components/ui/context-menu'
import { cn } from '@/lib/utils'
import { useUIStore } from '@/stores/ui'
import type { Item, SortField } from './api'
import {
  buildColumns,
  columnWidth,
  orderedVisible,
  type ColumnId,
  type ColumnMeta,
  type ColumnsPref,
} from './columns'
import {
  clear as clearSelection,
  click as clickSelect,
  headerState,
  isSelected,
  selectAll,
  selectRange,
  selectOnly,
  toggle,
  type SelectionState,
} from './SelectionModel'
import type { SortPref } from './sortCycle'

export interface DetailsViewProps {
  items: Item[]
  total: number
  path: string
  hasNextPage: boolean
  isFetchingNextPage: boolean
  fetchNextPage: () => void
  onOpen: (item: Item) => void
  onQuickLook: (item: Item) => void
  columns: ColumnsPref
  onColumnsChange: (next: ColumnsPref) => void
  /** Show the search/recent-only columns (location). */
  allowContextColumns?: boolean
  sort: SortPref
  onSortChange: (field: SortField, secondary: boolean) => void
  selection: SelectionState
  onSelectionChange: (next: SelectionState) => void
  activeIndex: number
  onActiveIndexChange: (index: number) => void
  /** Context-menu content for the header (column picker). */
  headerMenu?: ReactNode
  /** Initial scroll offset (restored on back navigation). */
  initialScroll?: number
  onScrollChange?: (offset: number) => void
  /** Hide the checkbox column (mobile). */
  compact?: boolean
}

const HEADER_HEIGHT = 32
const CHECK_WIDTH = 36
const MIN_WIDTH = 40
const TYPEAHEAD_RESET_MS = 1000

/** Virtualised Details table (TanStack Table + Virtual, SPEC §8.4/§9.5/§9.9):
 *  role=grid with aria-activedescendant, multi-select, sortable/resizable/
 *  reorderable headers, type-ahead. Sorting itself happens on the server. */
export function DetailsView({
  items,
  total,
  path,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
  onOpen,
  onQuickLook,
  columns,
  onColumnsChange,
  allowContextColumns = false,
  sort,
  onSortChange,
  selection,
  onSelectionChange,
  activeIndex,
  onActiveIndexChange,
  headerMenu,
  initialScroll = 0,
  onScrollChange,
  compact = false,
}: DetailsViewProps) {
  const { t, i18n } = useTranslation('files')
  const lang = i18n.resolvedLanguage ?? 'id'
  const density = useUIStore((s) => s.density)
  const rowHeight = density === 'comfortable' ? 40 : 36
  const allColumns = useMemo(() => buildColumns(t, lang), [t, lang])
  const visibleIds = useMemo(
    () => orderedVisible(columns, allowContextColumns),
    [columns, allowContextColumns],
  )
  const visible = useMemo(
    () => visibleIds.flatMap((id) => allColumns.filter((c) => c.id === id)),
    [visibleIds, allColumns],
  )
  const rows = items
  const scrollRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: 12,
  })
  const gridId = useRef(`grid-${Math.random().toString(36).slice(2, 8)}`).current
  const active = Math.min(activeIndex, Math.max(0, rows.length - 1))

  // live width while dragging a resize handle (committed to prefs on release)
  const [dragWidth, setDragWidth] = useState<{ id: ColumnId; width: number } | null>(null)
  const [dropTarget, setDropTarget] = useState<ColumnId | null>(null)

  // restore scroll on mount / path change
  useLayoutEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = initialScroll
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  // infinite scroll: prefetch when 80 % of loaded rows are rendered
  const virtualItems = virtualizer.getVirtualItems()
  const lastIndex = virtualItems[virtualItems.length - 1]?.index ?? 0
  useEffect(() => {
    if (!hasNextPage || isFetchingNextPage || rows.length === 0) return
    if (lastIndex >= Math.floor(rows.length * 0.8)) fetchNextPage()
  }, [lastIndex, rows.length, hasNextPage, isFetchingNextPage, fetchNextPage])

  const onScroll = useCallback(() => {
    const el = scrollRef.current
    if (el && onScrollChange) onScrollChange(el.scrollTop)
  }, [onScrollChange])

  const move = useCallback(
    (next: number, mod: { shift?: boolean; ctrl?: boolean } = {}) => {
      if (rows.length === 0) return
      const clamped = Math.max(0, Math.min(rows.length - 1, next))
      onActiveIndexChange(clamped)
      virtualizer.scrollToIndex(clamped, { align: 'auto' })
      const item = rows[clamped]!
      if (mod.shift) onSelectionChange(selectRange(selection, items, item.path, !!mod.ctrl))
      else if (!mod.ctrl) onSelectionChange(selectOnly(item.path))
    },
    [rows, items, selection, onActiveIndexChange, onSelectionChange, virtualizer],
  )

  // type-ahead: printable keys jump to the next item starting with the buffer
  const typeahead = useRef<{ buf: string; at: number }>({ buf: '', at: 0 })
  const typeAhead = (ch: string) => {
    const now = Date.now()
    const ta = typeahead.current
    const buf = now - ta.at < TYPEAHEAD_RESET_MS ? ta.buf + ch : ch
    typeahead.current = { buf, at: now }
    const needle = buf.toLowerCase()
    const repeated = buf.length > 1 && [...buf].every((c) => c === buf[0])
    // one (or a repeated) character cycles from the current row; a longer prefix
    // jumps to the first match from the top (PROMPT 7b AC)
    const start = repeated || buf.length === 1 ? active + 1 : 0
    for (let k = 0; k < rows.length; k++) {
      const i = (start + k) % rows.length
      const name = rows[i]!.name.toLowerCase()
      if (name.startsWith(repeated ? needle[0]! : needle)) {
        move(i)
        return
      }
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (rows.length === 0) return
    const mod = { shift: e.shiftKey, ctrl: e.ctrlKey || e.metaKey }
    const pageSize = Math.max(
      1,
      Math.floor((scrollRef.current?.clientHeight ?? 400) / rowHeight) - 1,
    )
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        move(active + 1, mod)
        return
      case 'ArrowUp':
        e.preventDefault()
        move(active - 1, mod)
        return
      case 'Home':
        e.preventDefault()
        move(0, mod)
        return
      case 'End':
        e.preventDefault()
        move(rows.length - 1, mod)
        return
      case 'PageDown':
        e.preventDefault()
        move(active + pageSize, mod)
        return
      case 'PageUp':
        e.preventDefault()
        move(active - pageSize, mod)
        return
      case 'Enter': {
        if (e.altKey) return // Alt+Enter = properties (global shortcut)
        const row = rows[active]
        if (row) {
          e.preventDefault()
          onOpen(row)
        }
        return
      }
      case ' ': {
        const row = rows[active]
        if (!row) return
        e.preventDefault()
        if (mod.ctrl) onSelectionChange(toggle(selection, row.path))
        else onQuickLook(row)
        return
      }
      default:
    }
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault()
      typeAhead(e.key)
    }
  }

  const widthOf = (id: ColumnId) =>
    dragWidth && dragWidth.id === id ? dragWidth.width : columnWidth(columns, id)
  const colStyle = (id: ColumnId, meta: ColumnMeta | undefined): React.CSSProperties => {
    const w = widthOf(id)
    return {
      width: w,
      flex: meta?.grow ? `1 1 ${w}px` : `0 0 ${w}px`,
      minWidth: MIN_WIDTH,
      textAlign: meta?.align ?? 'left',
      justifyContent: meta?.align === 'right' ? 'flex-end' : 'flex-start',
    }
  }

  // --- column resize (pointer drag on the header edge) ---
  const startResize = (e: React.PointerEvent, id: ColumnId) => {
    e.preventDefault()
    e.stopPropagation()
    const startX = e.clientX
    const startW = widthOf(id)
    const target = e.currentTarget as HTMLElement
    target.setPointerCapture(e.pointerId)
    let last = startW
    const onMove = (ev: PointerEvent) => {
      last = Math.max(MIN_WIDTH, Math.min(2000, startW + (ev.clientX - startX)))
      setDragWidth({ id, width: last })
    }
    const onUp = () => {
      target.removeEventListener('pointermove', onMove)
      target.removeEventListener('pointerup', onUp)
      target.removeEventListener('pointercancel', onUp)
      setDragWidth(null)
      if (last !== startW)
        onColumnsChange({ ...columns, widths: { ...columns.widths, [id]: last } })
    }
    target.addEventListener('pointermove', onMove)
    target.addEventListener('pointerup', onUp)
    target.addEventListener('pointercancel', onUp)
  }

  // --- column reorder (HTML5 drag of headers; name stays first) ---
  const onHeaderDrop = (e: React.DragEvent, targetId: ColumnId) => {
    e.preventDefault()
    const dragged = e.dataTransfer.getData('text/x-column') as ColumnId
    setDropTarget(null)
    if (!dragged || dragged === targetId || dragged === 'name' || targetId === 'name') return
    const order = columns.order.filter((c) => c !== dragged)
    const at = order.indexOf(targetId)
    order.splice(at, 0, dragged)
    onColumnsChange({ ...columns, order })
  }

  const header = headerState(selection, items.length, total)
  const toggleAll = () => onSelectionChange(header === 'all' ? clearSelection() : selectAll())

  const headerRow = (
    <div
      role="row"
      aria-rowindex={1}
      className="sticky top-0 z-10 flex border-b border-border bg-surface-2 text-xs font-medium text-text-muted"
      style={{ height: HEADER_HEIGHT }}
      data-testid="details-header"
    >
      {!compact ? (
        <div
          role="columnheader"
          className="flex shrink-0 items-center justify-center"
          style={{ width: CHECK_WIDTH }}
        >
          <Checkbox
            aria-label={t('selection.all')}
            checked={header === 'all' ? true : header === 'some' ? 'indeterminate' : false}
            onCheckedChange={toggleAll}
            data-testid="select-all"
          />
        </div>
      ) : null}
      {visible.map((c) => {
        const id = c.id
        const meta = c.meta
        const field = meta?.sort
        const isPrimary = field && sort.field === field
        const isSecondary = field && sort.field2 === field
        const dir = isPrimary ? sort.dir : isSecondary ? sort.dir2 : undefined
        return (
          <div
            key={c.id}
            role="columnheader"
            aria-sort={isPrimary ? (dir === 'desc' ? 'descending' : 'ascending') : 'none'}
            className={cn(
              'group relative flex items-center gap-1 truncate px-3 select-none',
              field && 'cursor-pointer hover:bg-border/60',
              dropTarget === id && 'bg-accent-subtle',
            )}
            style={colStyle(id, meta)}
            draggable={id !== 'name'}
            onDragStart={(e) => {
              e.dataTransfer.setData('text/x-column', id)
              e.dataTransfer.effectAllowed = 'move'
            }}
            onDragOver={(e) => {
              if (id !== 'name') {
                e.preventDefault()
                setDropTarget(id)
              }
            }}
            onDragLeave={() => setDropTarget((d) => (d === id ? null : d))}
            onDrop={(e) => onHeaderDrop(e, id)}
            onClick={(e) => {
              if (field) onSortChange(field, e.shiftKey)
            }}
            data-testid={`col-header-${id}`}
            data-sort={isPrimary ? dir : isSecondary ? `2-${dir}` : undefined}
          >
            <span className="truncate">{c.header()}</span>
            {dir ? (
              <span className="flex shrink-0 items-center text-accent" aria-hidden="true">
                {dir === 'desc' ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />}
                {isSecondary ? <span className="text-[10px] leading-none">2</span> : null}
              </span>
            ) : null}
            <span
              role="separator"
              aria-orientation="vertical"
              aria-label={t('columns.resize')}
              className="absolute top-0 right-0 h-full w-1.5 cursor-col-resize opacity-0 group-hover:opacity-100 hover:bg-accent"
              onPointerDown={(e) => startResize(e, id)}
              onClick={(e) => e.stopPropagation()}
              onDoubleClick={(e) => {
                e.stopPropagation()
                const widths = { ...columns.widths }
                delete widths[id]
                onColumnsChange({ ...columns, widths })
              }}
              data-testid={`col-resize-${id}`}
            />
          </div>
        )
      })}
    </div>
  )

  return (
    <div
      ref={scrollRef}
      onScroll={onScroll}
      className="relative h-full min-h-0 flex-1 overflow-auto bg-bg outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus"
      role="grid"
      aria-label={t('grid.label', { path })}
      aria-rowcount={total + 1}
      aria-colcount={visible.length + (compact ? 0 : 1)}
      aria-multiselectable="true"
      aria-activedescendant={rows.length > 0 ? `${gridId}-row-${active}` : undefined}
      tabIndex={0}
      onKeyDown={onKeyDown}
      data-testid="details-grid"
    >
      {headerMenu ? (
        <ContextMenu>
          <ContextMenuTrigger asChild>{headerRow}</ContextMenuTrigger>
          <ContextMenuContent className="w-56">{headerMenu}</ContextMenuContent>
        </ContextMenu>
      ) : (
        headerRow
      )}
      <div
        style={{ height: virtualizer.getTotalSize(), position: 'relative' }}
        data-testid="details-body"
      >
        {virtualItems.map((v) => {
          const item = rows[v.index]!
          const isActive = v.index === active
          const selected = isSelected(selection, item.path)
          return (
            <div
              key={item.path}
              id={`${gridId}-row-${v.index}`}
              role="row"
              aria-rowindex={v.index + 2}
              aria-selected={selected}
              data-index={v.index}
              data-path={item.path}
              className={cn(
                'absolute left-0 flex w-full cursor-default items-center border-b border-border/60 text-sm text-text select-none hover:bg-surface-2',
                selected && 'bg-accent-subtle hover:bg-accent-subtle',
                isActive && 'outline outline-1 -outline-offset-1 outline-accent/60',
              )}
              style={{ height: rowHeight, transform: `translateY(${v.start}px)` }}
              onClick={(e) => {
                onActiveIndexChange(v.index)
                onSelectionChange(
                  clickSelect(selection, items, item.path, {
                    ctrl: e.ctrlKey || e.metaKey,
                    shift: e.shiftKey,
                  }),
                )
              }}
              onDoubleClick={() => onOpen(item)}
            >
              {!compact ? (
                <div
                  role="gridcell"
                  className="flex shrink-0 items-center justify-center"
                  style={{ width: CHECK_WIDTH }}
                  onClick={(e) => {
                    e.stopPropagation()
                    onActiveIndexChange(v.index)
                    onSelectionChange(toggle(selection, item.path))
                  }}
                  onDoubleClick={(e) => e.stopPropagation()}
                >
                  <Checkbox
                    checked={selected}
                    aria-label={t('selection.row', { name: item.name })}
                    tabIndex={-1}
                    className={cn(!selected && 'opacity-40 hover:opacity-100')}
                  />
                </div>
              ) : null}
              {visible.map((c) => (
                <div
                  key={c.id}
                  role="gridcell"
                  className="flex min-w-0 items-center truncate px-3"
                  style={colStyle(c.id, c.meta)}
                >
                  {c.cell(item)}
                </div>
              ))}
            </div>
          )
        })}
      </div>
      {isFetchingNextPage ? (
        <div
          className="sticky bottom-0 bg-surface/90 px-3 py-1 text-center text-xs text-text-muted"
          role="status"
        >
          {t('grid.loadingMore')}
        </div>
      ) : null}
    </div>
  )
}
