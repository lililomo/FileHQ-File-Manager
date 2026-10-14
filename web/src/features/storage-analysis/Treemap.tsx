import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { formatBytes, formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { AnalysisNode } from './api'
import { squarify, type Rect } from './squarify'

export interface TreemapProps {
  node: AnalysisNode
  width: number
  height: number
  onOpen?: (node: AnalysisNode) => void
  className?: string
}

/** Colour classes per depth / kind (uses theme tokens: accent for folders,
 *  muted surfaces for file leaves). */
function fillClass(node: AnalysisNode, depth: number): string {
  if (node.kind === 'files') return 'fill-surface-2 stroke-border'
  if (node.kind === 'other') return 'fill-surface-2 stroke-border [fill-opacity:0.6]'
  return depth === 0
    ? 'fill-accent/80 stroke-bg hover:fill-accent'
    : depth === 1
      ? 'fill-accent/55 stroke-bg hover:fill-accent/70'
      : 'fill-accent/35 stroke-bg hover:fill-accent/50'
}

interface Cell {
  node: AnalysisNode
  rect: Rect
  depth: number
  label: string
}

const PAD = 2
const HEADER = 16

/** Lays the first two levels out: children of `node`, and inside each folder
 *  its children (one more level) so the picture conveys nesting. */
function layoutCells(
  root: AnalysisNode,
  width: number,
  height: number,
  t: (k: string) => string,
): Cell[] {
  const cells: Cell[] = []
  const kids = root.children ?? []
  const outer = squarify(kids, (n) => n.size, { x: 0, y: 0, w: width, h: height })
  for (const { item, rect } of outer) {
    const label =
      item.kind === 'files' ? t('directFiles') : item.kind === 'other' ? t('other') : item.name
    cells.push({ node: item, rect, depth: 0, label })
    const inner = item.children ?? []
    if (item.kind === 'dir' && inner.length > 0 && rect.w > 40 && rect.h > HEADER + 20) {
      const innerRect = {
        x: rect.x + PAD,
        y: rect.y + HEADER,
        w: rect.w - PAD * 2,
        h: rect.h - HEADER - PAD,
      }
      for (const sub of squarify(inner, (n) => n.size, innerRect)) {
        const l =
          sub.item.kind === 'files'
            ? t('directFiles')
            : sub.item.kind === 'other'
              ? t('other')
              : sub.item.name
        cells.push({ node: sub.item, rect: sub.rect, depth: 1, label: l })
      }
    }
  }
  return cells
}

export function Treemap({ node, width, height, onOpen, className }: TreemapProps) {
  const { t, i18n } = useTranslation('meta')
  const lang = i18n.resolvedLanguage ?? 'id'
  const [hover, setHover] = useState<Cell | null>(null)
  const tr = (k: string) => t(`storage.${k}` as 'storage.other')
  const cells = useMemo(
    () => (width > 0 && height > 0 ? layoutCells(node, width, height, tr) : []),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [node, width, height, lang],
  )
  const title = (c: Cell) =>
    `${c.label} — ${formatBytes(c.node.size, lang)} · ${t('storage.files', { count: c.node.files, n: formatNumber(c.node.files, lang) })}`
  return (
    <div className={cn('relative', className)} data-testid="treemap">
      <svg
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        role="img"
        aria-label={t('storage.treemap')}
        className="block overflow-hidden rounded-md"
      >
        {cells.map((c, i) => {
          const clickable = c.node.kind === 'dir' && !!onOpen
          const showLabel = c.rect.w > 48 && c.rect.h > 14
          return (
            <g
              key={`${c.depth}-${c.node.path}-${c.node.kind}-${i}`}
              className={cn(clickable && 'cursor-pointer')}
              onClick={() => clickable && onOpen?.(c.node)}
              onMouseEnter={() => setHover(c)}
              onMouseLeave={() => setHover(null)}
              onFocus={() => setHover(c)}
              onBlur={() => setHover(null)}
              tabIndex={clickable ? 0 : -1}
              onKeyDown={(e) => {
                if (clickable && (e.key === 'Enter' || e.key === ' ')) {
                  e.preventDefault()
                  onOpen?.(c.node)
                }
              }}
              role={clickable ? 'button' : undefined}
              aria-label={title(c)}
              data-testid="treemap-cell"
              data-kind={c.node.kind}
              data-path={c.node.path}
              data-depth={c.depth}
            >
              <title>{title(c)}</title>
              <rect
                x={c.rect.x + (c.depth === 1 ? 0 : 0.5)}
                y={c.rect.y + 0.5}
                width={Math.max(0, c.rect.w - 1)}
                height={Math.max(0, c.rect.h - 1)}
                rx={2}
                className={fillClass(c.node, c.depth)}
                strokeWidth={1}
              />
              {showLabel ? (
                <text
                  x={c.rect.x + 4}
                  y={c.rect.y + 12}
                  className={cn(
                    'pointer-events-none select-none text-[11px]',
                    c.depth === 0 && c.node.kind === 'dir' ? 'fill-accent-fg' : 'fill-text',
                  )}
                >
                  <tspan>{truncate(c.label, c.rect.w)}</tspan>
                </text>
              ) : null}
            </g>
          )
        })}
      </svg>
      {hover ? (
        <div
          className="pointer-events-none absolute z-10 max-w-xs rounded-md border border-border bg-surface px-2 py-1 text-xs text-text shadow-md"
          style={{
            left: Math.min(hover.rect.x + 8, Math.max(0, width - 220)),
            top: Math.min(hover.rect.y + hover.rect.h + 4, Math.max(0, height - 48)),
          }}
          role="tooltip"
          data-testid="treemap-tooltip"
        >
          <div className="font-medium">{hover.label}</div>
          <div className="text-text-muted">
            {formatBytes(hover.node.size, lang)} ·{' '}
            {t('storage.files', {
              count: hover.node.files,
              n: formatNumber(hover.node.files, lang),
            })}
            {hover.node.kind === 'dir' && hover.node.dirs > 0
              ? ` · ${t('storage.dirs', { count: hover.node.dirs, n: formatNumber(hover.node.dirs, lang) })}`
              : ''}
          </div>
          <div className="font-mono text-[10px] text-text-muted">{hover.node.path}</div>
        </div>
      ) : null}
    </div>
  )
}

function truncate(label: string, width: number): string {
  const max = Math.max(1, Math.floor((width - 8) / 6.5))
  return label.length > max ? `${label.slice(0, Math.max(1, max - 1))}…` : label
}
