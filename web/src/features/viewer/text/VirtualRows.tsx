import { useVirtualizer, type Virtualizer } from '@tanstack/react-virtual'
import { useEffect, useRef, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

export interface VirtualRowsProps {
  count: number
  /** fixed row height, or the estimate when `measure` is on */
  rowHeight: number
  /** rows of different heights (wrapped text): measured after render */
  measure?: boolean
  renderRow: (index: number) => ReactNode
  /** sticky content above the rows (table header) */
  header?: ReactNode
  className?: string
  /** minimum scroll width (wide tables) */
  minWidth?: number | undefined
  /** receives the virtualizer (scrollToIndex from search) */
  onReady?: (v: Virtualizer<HTMLDivElement, Element>) => void
  testId?: string
  role?: string
  label?: string
}

/** One virtualised scroller shared by the text, CSV and JSON viewers
 *  (Step 17b): only the visible rows (+ overscan) are in the DOM, so 100k
 *  CSV rows or a 5 MB JSON tree scroll without freezing. */
export function VirtualRows({
  count,
  rowHeight,
  measure = false,
  renderRow,
  header,
  className,
  minWidth,
  onReady,
  testId,
  role,
  label,
}: VirtualRowsProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  // TanStack Virtual returns functions the React Compiler cannot memoise;
  // this component is the single place the viewers use it.
  // eslint-disable-next-line react-hooks/incompatible-library
  const v = useVirtualizer({
    count,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: 20,
    // first paint before the scroller is measured (and in jsdom)
    initialRect: { width: 1024, height: 768 },
  })
  useEffect(() => {
    onReady?.(v)
  }, [v, onReady])
  return (
    <div
      ref={scrollRef}
      className={cn('min-h-0 flex-1 overflow-auto', className)}
      data-testid={testId}
      role={role}
      aria-label={label}
      aria-rowcount={role ? count : undefined}
    >
      <div style={minWidth ? { minWidth } : undefined}>
        {header}
        <div className="relative w-full" style={{ height: v.getTotalSize() }}>
          {v.getVirtualItems().map((row) => (
            <div
              key={row.key}
              data-index={row.index}
              ref={measure ? v.measureElement : undefined}
              className="absolute left-0 w-full"
              style={{ top: row.start, ...(measure ? {} : { height: rowHeight }) }}
            >
              {renderRow(row.index)}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
