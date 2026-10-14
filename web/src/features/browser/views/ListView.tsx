import { useVirtualizer } from '@tanstack/react-virtual'
import { Link2Off } from 'lucide-react'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { ItemBadges } from '../Badges'
import { toIconCategory } from '@/components/file-icon/categories'
import {
  autoScrollOnDrag,
  dropTargetProps,
  endItemDrag,
  startItemDrag,
} from '@/features/file-ops/dnd'
import { cn } from '@/lib/utils'
import { useUIStore } from '@/stores/ui'
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
import { listCellsInRect, listLayout, moveInList } from './gridMath'
import { useMarquee } from './useMarquee'
import { useTypeAhead } from './useTypeAhead'
import type { ViewOpsProps } from './opsProps'
import { useSlowClick } from './useSlowClick'

export interface ListViewProps extends ViewOpsProps {
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
  initialScroll?: number
  onScrollChange?: (offset: number) => void
}

const COL_WIDTH = 240

/** List view (SPEC §8.4): 16 px icon + name flowing top→bottom then left→right
 *  like Windows Explorer; the column height follows the container and columns
 *  are virtualised horizontally. */
export function ListView({
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
  initialScroll = 0,
  onScrollChange,
  renamingPath = null,
  renderRename,
  cutPaths,
  onSlowClick,
  dragPaths,
  dnd = true,
}: ListViewProps) {
  const { t } = useTranslation('files')
  const slowClick = useSlowClick(onSlowClick)
  const [dropOver, setDropOver] = useState<string | null>(null)
  const density = useUIStore((s) => s.density)
  const rowH = density === 'comfortable' ? 28 : 24
  const scrollRef = useRef<HTMLDivElement>(null)
  const [height, setHeight] = useState(0)
  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el) return
    setHeight(el.clientHeight)
    const ro = new ResizeObserver(() => setHeight(el.clientHeight))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  const layout = useMemo(
    () => listLayout(Math.max(rowH, height - 8), rowH, COL_WIDTH, items.length),
    [height, rowH, items.length],
  )
  const virtualizer = useVirtualizer({
    horizontal: true,
    count: layout.columns,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => COL_WIDTH,
    overscan: 2,
    paddingStart: 8,
    paddingEnd: 8,
  })
  const gridId = useRef(`list-${Math.random().toString(36).slice(2, 8)}`).current
  const active = Math.min(activeIndex, Math.max(0, items.length - 1))

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollLeft = initialScroll
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  const cols = virtualizer.getVirtualItems()
  const lastCol = cols[cols.length - 1]?.index ?? 0
  useEffect(() => {
    if (!hasNextPage || isFetchingNextPage || layout.columns === 0) return
    if (lastCol >= Math.floor(layout.columns * 0.8)) fetchNextPage()
  }, [lastCol, layout.columns, hasNextPage, isFetchingNextPage, fetchNextPage])

  const move = useCallback(
    (next: number, mod: { shift?: boolean; ctrl?: boolean } = {}) => {
      if (items.length === 0) return
      const clamped = Math.max(0, Math.min(items.length - 1, next))
      onActiveIndexChange(clamped)
      virtualizer.scrollToIndex(Math.floor(clamped / layout.rowsPerColumn), { align: 'auto' })
      const item = items[clamped]!
      if (mod.shift) onSelectionChange(selectRange(selection, items, item.path, !!mod.ctrl))
      else if (!mod.ctrl) onSelectionChange(selectOnly(item.path))
    },
    [items, selection, onActiveIndexChange, onSelectionChange, virtualizer, layout.rowsPerColumn],
  )
  const typeAhead = useTypeAhead(
    () => items.map((it) => it.name),
    () => active,
    (i) => move(i),
  )
  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (items.length === 0) return
    const mod = { shift: e.shiftKey, ctrl: e.ctrlKey || e.metaKey }
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
        move(moveInList(layout, active, e.key), mod)
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
    toIndices: (r) => listCellsInRect(layout, { ...r, x: r.x - 8 }),
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
      className="relative h-full min-h-0 flex-1 overflow-x-auto overflow-y-hidden bg-bg outline-none select-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-focus"
      role="grid"
      aria-label={t('grid.label', { path })}
      aria-rowcount={layout.rowsPerColumn}
      aria-colcount={layout.columns}
      aria-multiselectable="true"
      aria-activedescendant={items.length > 0 ? `${gridId}-cell-${active}` : undefined}
      tabIndex={0}
      onKeyDown={onKeyDown}
      onScroll={() => onScrollChange?.(scrollRef.current?.scrollLeft ?? 0)}
      onPointerDown={marquee.onPointerDown}
      data-testid="list-grid"
      data-columns={layout.columns}
      data-rows={layout.rowsPerColumn}
      {...(dnd ? dropTargetProps({ dir: path }) : {})}
      onDragOver={(e) => {
        if (dnd) {
          dropTargetProps({ dir: path }).onDragOver(e)
          autoScrollOnDrag(e.currentTarget, e.clientY)
        }
      }}
    >
      <div
        style={{ width: virtualizer.getTotalSize(), height: '100%', position: 'relative' }}
        data-testid="list-body"
      >
        {cols.map((col) => {
          const start = col.index * layout.rowsPerColumn
          const cells = items.slice(start, start + layout.rowsPerColumn)
          return (
            <div
              key={col.index}
              role="presentation"
              className="absolute top-0 flex flex-col pt-1"
              style={{ transform: `translateX(${col.start}px)`, width: COL_WIDTH }}
            >
              {cells.map((item, row) => {
                const index = start + row
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
                    role="row"
                    aria-rowindex={row + 1}
                    aria-selected={selected}
                    data-cell
                    data-index={index}
                    data-path={item.path}
                    data-cut={isCut || undefined}
                    data-drop-over={dropOver === item.path || undefined}
                    className={cn(
                      'mx-1 flex cursor-default items-center gap-2 rounded px-2 text-sm text-text hover:bg-surface-2',
                      selected && 'bg-accent-subtle hover:bg-accent-subtle',
                      isActive && 'outline outline-1 -outline-offset-1 outline-accent/60',
                      isCut && 'opacity-50',
                      dropOver === item.path &&
                        'bg-accent/15 outline outline-2 -outline-offset-2 outline-accent',
                    )}
                    style={{ height: rowH }}
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
                    <span
                      role="gridcell"
                      className="flex min-w-0 flex-1 items-center gap-2"
                      data-name
                    >
                      <FileIcon
                        category={item.isDir ? 'folder' : toIconCategory(item.category)}
                        size={16}
                      />
                      {renaming && renderRename ? (
                        renderRename(item)
                      ) : (
                        <span className="truncate" title={item.name}>
                          {item.name}
                        </span>
                      )}
                      {item.broken ? (
                        <Link2Off
                          className="size-3.5 shrink-0 text-danger"
                          aria-label={t('grid.broken')}
                        />
                      ) : null}
                      <ItemBadges item={item} />
                    </span>
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
          className="absolute right-2 bottom-1 rounded bg-surface/90 px-2 py-0.5 text-xs text-text-muted"
          role="status"
        >
          {t('grid.loadingMore')}
        </div>
      ) : null}
      <span className="sr-only">{t('grid.count', { count: total })}</span>
    </div>
  )
}
