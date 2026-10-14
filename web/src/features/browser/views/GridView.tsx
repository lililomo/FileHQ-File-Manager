import { useVirtualizer } from '@tanstack/react-virtual'
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
import {
  autoScrollOnDrag,
  dropTargetProps,
  endItemDrag,
  startItemDrag,
} from '@/features/file-ops/dnd'
import { cn } from '@/lib/utils'
import type { Item } from '../api'
import {
  clear as clearSelection,
  click as clickSelect,
  isSelected,
  selectRange,
  selectOnly,
  toggle,
  type SelectionState,
} from '../SelectionModel'
import { cellsInRect, gridLayout, moveInGrid, type GridSpec } from './gridMath'
import { useMarquee } from './useMarquee'
import { useTypeAhead } from './useTypeAhead'
import type { ViewOpsProps } from './opsProps'
import { useSlowClick } from './useSlowClick'

export interface CellState {
  selected: boolean
  active: boolean
  /** Inline rename in progress: render `renameSlot` instead of the name. */
  renameSlot?: ReactNode
}

export interface GridViewProps extends ViewOpsProps {
  items: Item[]
  total: number
  path: string
  hasNextPage: boolean
  isFetchingNextPage: boolean
  fetchNextPage: () => void
  onOpen: (item: Item) => void
  onQuickLook: (item: Item) => void
  selection: SelectionState
  onSelectionChange: (next: SelectionState) => void
  activeIndex: number
  onActiveIndexChange: (index: number) => void
  spec: GridSpec
  renderCell: (item: Item, state: CellState) => ReactNode
  initialScroll?: number
  onScrollChange?: (offset: number) => void
  /** data-testid / aria name of the view */
  view: string
}

/** Virtualised icon grid (Tiles / Large icons / Extra large icons, SPEC §8.4):
 *  rows are virtualised, columns derive from the container width; keyboard
 *  moves in two dimensions; marquee selection on the background. */
export function GridView({
  items,
  total,
  path,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
  onOpen,
  onQuickLook,
  selection,
  onSelectionChange,
  activeIndex,
  onActiveIndexChange,
  spec,
  renderCell,
  initialScroll = 0,
  onScrollChange,
  view,
  renamingPath = null,
  renderRename,
  cutPaths,
  onSlowClick,
  dragPaths,
  dnd = true,
}: GridViewProps) {
  const { t } = useTranslation('files')
  const slowClick = useSlowClick(onSlowClick)
  const [dropOver, setDropOver] = useState<string | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el) return
    setWidth(el.clientWidth)
    const ro = new ResizeObserver(() => setWidth(el.clientWidth))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  const layout = useMemo(() => gridLayout(spec, width, items.length), [spec, width, items.length])
  const rowStride = spec.cellH + spec.gap
  const virtualizer = useVirtualizer({
    count: layout.rows,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowStride,
    overscan: 3,
    paddingStart: spec.padding,
    paddingEnd: spec.padding,
  })
  const gridId = useRef(`grid-${Math.random().toString(36).slice(2, 8)}`).current
  const active = Math.min(activeIndex, Math.max(0, items.length - 1))

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = initialScroll
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  const virtualRows = virtualizer.getVirtualItems()
  const lastRow = virtualRows[virtualRows.length - 1]?.index ?? 0
  useEffect(() => {
    if (!hasNextPage || isFetchingNextPage || layout.rows === 0) return
    if (lastRow >= Math.floor(layout.rows * 0.8)) fetchNextPage()
  }, [lastRow, layout.rows, hasNextPage, isFetchingNextPage, fetchNextPage])

  const scrollToIndex = useCallback(
    (i: number) => virtualizer.scrollToIndex(Math.floor(i / layout.columns), { align: 'auto' }),
    [virtualizer, layout.columns],
  )
  const move = useCallback(
    (next: number, mod: { shift?: boolean; ctrl?: boolean } = {}) => {
      if (items.length === 0) return
      const clamped = Math.max(0, Math.min(items.length - 1, next))
      onActiveIndexChange(clamped)
      scrollToIndex(clamped)
      const item = items[clamped]!
      if (mod.shift) onSelectionChange(selectRange(selection, items, item.path, !!mod.ctrl))
      else if (!mod.ctrl) onSelectionChange(selectOnly(item.path))
    },
    [items, selection, onActiveIndexChange, onSelectionChange, scrollToIndex],
  )
  const typeAhead = useTypeAhead(
    () => items.map((it) => it.name),
    () => active,
    (i) => move(i),
  )

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (items.length === 0) return
    const mod = { shift: e.shiftKey, ctrl: e.ctrlKey || e.metaKey }
    const pageRows = Math.max(
      1,
      Math.floor((scrollRef.current?.clientHeight ?? 400) / rowStride) - 1,
    )
    switch (e.key) {
      case 'ArrowLeft':
      case 'ArrowRight':
      case 'ArrowUp':
      case 'ArrowDown':
      case 'Home':
      case 'End':
      case 'PageUp':
      case 'PageDown':
        e.preventDefault()
        move(moveInGrid(layout, active, e.key, pageRows), mod)
        return
      case 'Enter': {
        if (e.altKey) return
        const it = items[active]
        if (it) {
          e.preventDefault()
          onOpen(it)
        }
        return
      }
      case ' ': {
        const it = items[active]
        if (!it) return
        e.preventDefault()
        if (mod.ctrl) onSelectionChange(toggle(selection, it.path))
        else onQuickLook(it)
        return
      }
      default:
    }
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault()
      typeAhead(e.key)
    }
  }

  const marquee = useMarquee({
    enabled: true,
    isBackground: (target) => !(target instanceof HTMLElement && target.closest('[data-cell]')),
    toIndices: (r) => cellsInRect(layout, r),
    onSelect: (indices, additive) => {
      const paths = new Set(indices.map((i) => items[i]!.path))
      if (additive) for (const it of items) if (isSelected(selection, it.path)) paths.add(it.path)
      onSelectionChange({ mode: 'explicit', paths, excluded: new Set(), anchor: selection.anchor })
    },
    onBackgroundClick: (additive) => {
      if (!additive) onSelectionChange(clearSelection())
    },
  })

  return (
    <div
      ref={scrollRef}
      className="relative h-full min-h-0 flex-1 overflow-auto bg-bg outline-none select-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus"
      role="grid"
      aria-label={t('grid.label', { path })}
      aria-rowcount={layout.rows}
      aria-colcount={layout.columns}
      aria-multiselectable="true"
      aria-activedescendant={items.length > 0 ? `${gridId}-cell-${active}` : undefined}
      tabIndex={0}
      onKeyDown={onKeyDown}
      onScroll={() => onScrollChange?.(scrollRef.current?.scrollTop ?? 0)}
      onPointerDown={marquee.onPointerDown}
      data-testid={`${view}-grid`}
      data-columns={layout.columns}
      {...(dnd ? dropTargetProps({ dir: path }) : {})}
      onDragOver={(e) => {
        if (dnd) {
          dropTargetProps({ dir: path }).onDragOver(e)
          autoScrollOnDrag(e.currentTarget, e.clientY)
        }
      }}
    >
      <div
        style={{ height: virtualizer.getTotalSize(), position: 'relative' }}
        data-testid={`${view}-body`}
      >
        {virtualRows.map((row) => {
          const start = row.index * layout.columns
          const cells = items.slice(start, start + layout.columns)
          return (
            <div
              key={row.index}
              role="row"
              aria-rowindex={row.index + 1}
              className="absolute left-0 flex w-full"
              style={{
                transform: `translateY(${row.start}px)`,
                height: spec.cellH,
                paddingLeft: spec.padding,
                gap: spec.gap,
              }}
            >
              {cells.map((item, col) => {
                const index = start + col
                const selected = isSelected(selection, item.path)
                const isActive = index === active
                const wasOnlySelected =
                  selected && selection.mode === 'explicit' && selection.paths.size === 1
                const renaming = renamingPath === item.path
                const isCut = cutPaths?.has(item.path) ?? false
                const dropProps =
                  dnd && item.isDir && !renaming
                    ? dropTargetProps({ dir: item.path, onOver: setDropOver })
                    : {}
                return (
                  <div
                    key={item.path}
                    id={`${gridId}-cell-${index}`}
                    role="gridcell"
                    aria-selected={selected}
                    aria-colindex={col + 1}
                    data-cell
                    data-index={index}
                    data-path={item.path}
                    data-cut={isCut || undefined}
                    data-drop-over={dropOver === item.path || undefined}
                    className={cn(
                      'relative shrink-0 cursor-default rounded-md border border-transparent text-sm text-text',
                      'hover:bg-surface-2',
                      selected && 'border-accent/40 bg-accent-subtle hover:bg-accent-subtle',
                      isActive && 'outline outline-1 -outline-offset-1 outline-accent/60',
                      isCut && 'opacity-50',
                      dropOver === item.path && 'border-accent bg-accent/15',
                    )}
                    style={{ width: spec.cellW, height: spec.cellH }}
                    draggable={dnd && !renaming}
                    onDragStart={(e) => {
                      if (!dnd) return
                      const paths = dragPaths?.(item) ?? [item.path]
                      const label = (n: number) => t('grid.dragLabel', { count: n, n: String(n) })
                      if (!paths.includes(item.path)) {
                        onSelectionChange(selectOnly(item.path))
                        startItemDrag(e, { paths: [item.path], from: path }, label(1))
                        return
                      }
                      startItemDrag(e, { paths, from: path }, label(paths.length))
                    }}
                    onDragEnd={endItemDrag}
                    {...dropProps}
                    onClick={(e) => {
                      if (renaming) return
                      slowClick.onClick(e, item, wasOnlySelected)
                      onActiveIndexChange(index)
                      onSelectionChange(
                        clickSelect(selection, items, item.path, {
                          ctrl: e.ctrlKey || e.metaKey,
                          shift: e.shiftKey,
                        }),
                      )
                    }}
                    onDoubleClick={() => {
                      slowClick.cancel()
                      if (!renaming) onOpen(item)
                    }}
                  >
                    {renderCell(item, {
                      selected,
                      active: isActive,
                      renameSlot: renaming && renderRename ? renderRename(item) : undefined,
                    })}
                  </div>
                )
              })}
            </div>
          )
        })}
      </div>
      {marquee.rect ? (
        <div
          aria-hidden="true"
          className="pointer-events-none absolute z-10 border border-accent bg-accent/10"
          style={{
            left: marquee.rect.x,
            top: marquee.rect.y,
            width: marquee.rect.w,
            height: marquee.rect.h,
          }}
          data-testid="marquee"
        />
      ) : null}
      {isFetchingNextPage ? (
        <div
          className="sticky bottom-0 bg-surface/90 px-3 py-1 text-center text-xs text-text-muted"
          role="status"
        >
          {t('grid.loadingMore')}
        </div>
      ) : null}
      <span className="sr-only">{t('grid.count', { count: total })}</span>
    </div>
  )
}
