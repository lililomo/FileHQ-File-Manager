import { memo, type CSSProperties, type DragEvent, type MouseEvent, type ReactNode } from 'react'
import { dropTargetProps, endItemDrag } from '@/features/file-ops/dnd'
import { cn } from '@/lib/utils'
import type { Item } from './api'
import type { ColumnDef, ColumnId, ColumnMeta } from './columns'

/** Row callbacks of DetailsView. The object is stable for the life of the
 *  view (it forwards to the latest handlers), so memoised rows only re-render
 *  when their own data changes (Step 21: scroll without long tasks). */
export interface RowHandlers {
  click(e: MouseEvent, item: Item, index: number, wasOnlySelected: boolean): void
  doubleClick(e: MouseEvent, item: Item, renaming: boolean): void
  check(e: MouseEvent, item: Item, index: number): void
  dragStart(e: DragEvent, item: Item): void
  dropOver(dir: string | null): void
}

export interface DetailsRowProps {
  item: Item
  index: number
  start: number
  height: number
  rowId: string
  selected: boolean
  /** selected and the only explicit selection (slow click renames) */
  onlySelected: boolean
  active: boolean
  cut: boolean
  dropOver: boolean
  renaming: boolean
  renderRename: ((item: Item) => ReactNode) | undefined
  columns: ColumnDef[]
  colStyle: (id: ColumnId, meta: ColumnMeta | undefined) => CSSProperties
  checkLabel: (name: string) => string
  compact: boolean
  dnd: boolean
  checkWidth: number
  handlers: RowHandlers
}

/** One virtualised Details row (SPEC §9.9: role=row with aria-selected and
 *  aria-rowindex; the grid container owns focus via aria-activedescendant). */
export const DetailsRow = memo(function DetailsRow({
  item,
  index,
  start,
  height,
  rowId,
  selected,
  onlySelected,
  active,
  cut,
  dropOver,
  renaming,
  renderRename,
  columns,
  colStyle,
  checkLabel,
  compact,
  dnd,
  checkWidth,
  handlers,
}: DetailsRowProps) {
  const dropProps =
    dnd && item.isDir && !renaming
      ? dropTargetProps({ dir: item.path, onOver: handlers.dropOver })
      : {}
  return (
    <div
      id={rowId}
      role="row"
      aria-rowindex={index + 2}
      aria-selected={selected}
      data-index={index}
      data-path={item.path}
      data-cut={cut || undefined}
      data-drop-over={dropOver || undefined}
      className={cn(
        'absolute left-0 flex w-full cursor-default items-center border-b border-border/60 text-sm text-text select-none hover:bg-surface-2',
        selected && 'bg-accent-subtle hover:bg-accent-subtle',
        active && 'outline outline-1 -outline-offset-1 outline-accent/60',
        cut && 'opacity-50',
        dropOver && 'bg-accent/15 outline outline-2 -outline-offset-2 outline-accent',
      )}
      style={{ height, transform: `translateY(${start}px)` }}
      draggable={dnd && !renaming}
      onDragStart={(e) => {
        if (dnd) handlers.dragStart(e, item)
      }}
      onDragEnd={endItemDrag}
      {...dropProps}
      onClick={(e) => {
        if (!renaming) handlers.click(e, item, index, onlySelected)
      }}
      onDoubleClick={(e) => handlers.doubleClick(e, item, renaming)}
    >
      {!compact ? (
        <div
          role="gridcell"
          className="flex shrink-0 items-center justify-center"
          style={{ width: checkWidth }}
          onClick={(e) => handlers.check(e, item, index)}
          onDoubleClick={(e) => e.stopPropagation()}
        >
          {/* a plain element styled like ui/Checkbox: one per visible row, so the
              Radix primitive (button + presence + bubble input) is not worth it here;
              the grid keeps keyboard focus, this box is pointer-only (tabIndex -1) */}
          <span
            role="checkbox"
            aria-checked={selected}
            aria-label={checkLabel(item.name)}
            tabIndex={-1}
            data-state={selected ? 'checked' : 'unchecked'}
            className={cn(
              'grid size-4 shrink-0 place-content-center rounded-[4px] border border-input shadow-xs',
              selected
                ? 'border-primary bg-primary text-primary-foreground'
                : 'opacity-40 hover:opacity-100',
            )}
          >
            {selected ? (
              <svg viewBox="0 0 24 24" className="size-3.5" aria-hidden="true">
                <path
                  d="M20 6 9 17l-5-5"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            ) : null}
          </span>
        </div>
      ) : null}
      {columns.map((c) => (
        <div
          key={c.id}
          role="gridcell"
          className="flex min-w-0 items-center truncate px-3"
          style={colStyle(c.id, c.meta)}
        >
          {c.id === 'name' && renaming && renderRename ? renderRename(item) : c.cell(item)}
        </div>
      ))}
    </div>
  )
})
