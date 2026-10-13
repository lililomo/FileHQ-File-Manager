import { flexRender, useTable } from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { useUIStore } from '@/stores/ui'
import type { Item } from './api'
import {
  buildColumns,
  DEFAULT_COLUMNS,
  tableFeatureSet,
  type ColumnId,
  type ColumnMeta,
} from './columns'

export interface DetailsViewProps {
  items: Item[]
  total: number
  path: string
  hasNextPage: boolean
  isFetchingNextPage: boolean
  fetchNextPage: () => void
  onOpen: (item: Item) => void
  columns?: ColumnId[]
  /** Initial scroll offset (restored on back navigation). */
  initialScroll?: number
  onScrollChange?: (offset: number) => void
}

const HEADER_HEIGHT = 32

/** Virtualised Details table (TanStack Table + Virtual, SPEC §8.4/§9.9: role=grid,
 *  aria-activedescendant keyboard model so focus survives row recycling). */
export function DetailsView({
  items,
  total,
  path,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
  onOpen,
  columns = DEFAULT_COLUMNS,
  initialScroll = 0,
  onScrollChange,
}: DetailsViewProps) {
  const { t, i18n } = useTranslation('files')
  const lang = i18n.resolvedLanguage ?? 'id'
  const density = useUIStore((s) => s.density)
  const rowHeight = density === 'comfortable' ? 40 : 36
  const allColumns = useMemo(() => buildColumns(t, lang), [t, lang])
  const visible = useMemo(
    () => columns.flatMap((id) => allColumns.filter((c) => c.id === id)),
    [columns, allColumns],
  )
  const table = useTable({
    features: tableFeatureSet,
    data: items,
    columns: visible,
    getRowId: (row: Item) => row.path,
  })
  const rows = table.getRowModel().rows
  const scrollRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: 12,
  })
  const [active, setActive] = useState(0)
  const gridId = useRef(`grid-${Math.random().toString(36).slice(2, 8)}`).current

  // restore scroll on mount / path change
  useLayoutEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = initialScroll
    setActive(0)
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

  const move = (next: number) => {
    const clamped = Math.max(0, Math.min(rows.length - 1, next))
    setActive(clamped)
    virtualizer.scrollToIndex(clamped, { align: 'auto' })
  }
  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (rows.length === 0) return
    const pageSize = Math.max(
      1,
      Math.floor((scrollRef.current?.clientHeight ?? 400) / rowHeight) - 1,
    )
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        move(active + 1)
        break
      case 'ArrowUp':
        e.preventDefault()
        move(active - 1)
        break
      case 'Home':
        e.preventDefault()
        move(0)
        break
      case 'End':
        e.preventDefault()
        move(rows.length - 1)
        break
      case 'PageDown':
        e.preventDefault()
        move(active + pageSize)
        break
      case 'PageUp':
        e.preventDefault()
        move(active - pageSize)
        break
      case 'Enter': {
        const row = rows[active]
        if (row) {
          e.preventDefault()
          onOpen(row.original)
        }
        break
      }
      default:
    }
  }

  const colStyle = (meta: ColumnMeta | undefined): React.CSSProperties => ({
    width: meta?.width ?? 120,
    flex: meta?.grow ? '1 1 0%' : `0 0 ${meta?.width ?? 120}px`,
    textAlign: meta?.align ?? 'left',
    justifyContent: meta?.align === 'right' ? 'flex-end' : 'flex-start',
  })

  return (
    <div
      ref={scrollRef}
      onScroll={onScroll}
      className="relative h-full min-h-0 flex-1 overflow-auto bg-bg outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus"
      role="grid"
      aria-label={t('grid.label', { path })}
      aria-rowcount={total + 1}
      aria-colcount={visible.length}
      aria-activedescendant={rows.length > 0 ? `${gridId}-row-${active}` : undefined}
      tabIndex={0}
      onKeyDown={onKeyDown}
      data-testid="details-grid"
    >
      <div
        role="row"
        aria-rowindex={1}
        className="sticky top-0 z-10 flex border-b border-border bg-surface-2 text-xs font-medium text-text-muted"
        style={{ height: HEADER_HEIGHT }}
      >
        {table.getHeaderGroups()[0]?.headers.map((h) => (
          <div
            key={h.id}
            role="columnheader"
            className="flex items-center truncate px-3"
            style={colStyle(h.column.columnDef.meta)}
          >
            {flexRender(h.column.columnDef.header, h.getContext())}
          </div>
        ))}
      </div>
      <div
        style={{ height: virtualizer.getTotalSize(), position: 'relative' }}
        data-testid="details-body"
      >
        {virtualItems.map((v) => {
          const row = rows[v.index]!
          const isActive = v.index === active
          return (
            <div
              key={row.id}
              id={`${gridId}-row-${v.index}`}
              role="row"
              aria-rowindex={v.index + 2}
              data-index={v.index}
              data-path={row.original.path}
              className={cn(
                'absolute left-0 flex w-full cursor-default items-center border-b border-border/60 text-sm text-text select-none hover:bg-surface-2',
                isActive &&
                  'bg-accent-subtle/60 outline outline-1 -outline-offset-1 outline-accent/60',
              )}
              style={{ height: rowHeight, transform: `translateY(${v.start}px)` }}
              onClick={() => setActive(v.index)}
              onDoubleClick={() => onOpen(row.original)}
            >
              {row.getAllCells().map((cell) => (
                <div
                  key={cell.id}
                  role="gridcell"
                  className="flex min-w-0 items-center truncate px-3"
                  style={colStyle(cell.column.columnDef.meta)}
                >
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
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
