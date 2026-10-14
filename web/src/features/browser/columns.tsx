import type { TFunction } from 'i18next'
import type { ReactNode } from 'react'
import { Link2Off } from 'lucide-react'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  formatBytes,
  formatDateTimeFull,
  formatDuration,
  formatNumber,
  formatRelativeTime,
} from '@/lib/format'
import type { Item, SortField } from './api'

/** All Details-view columns (SPEC §8.4, brief §5). `location` only appears in
 *  search results / Recent (Step 15, 10b). */
export type ColumnId =
  | 'name'
  | 'size'
  | 'type'
  | 'mtime'
  | 'note'
  | 'btime'
  | 'atime'
  | 'ext'
  | 'mode'
  | 'itemCount'
  | 'dimensions'
  | 'duration'
  | 'shared'
  | 'location'

export const ALL_COLUMNS: ColumnId[] = [
  'name',
  'size',
  'type',
  'mtime',
  'note',
  'btime',
  'atime',
  'ext',
  'mode',
  'itemCount',
  'dimensions',
  'duration',
  'shared',
  'location',
]
export const DEFAULT_COLUMNS: ColumnId[] = ['name', 'size', 'type', 'mtime', 'note']
export const MOBILE_COLUMNS: ColumnId[] = ['name', 'mtime']
/** Columns that exist only in search/recent contexts. */
export const CONTEXT_COLUMNS: ColumnId[] = ['location']

export interface ColumnMeta {
  width: number
  align?: 'left' | 'right'
  grow?: boolean
  /** server sort field, undefined = not sortable */
  sort?: SortField
  mono?: boolean
}

export const COLUMN_META: Record<ColumnId, ColumnMeta> = {
  name: { width: 320, grow: true, sort: 'name' },
  size: { width: 96, align: 'right', sort: 'size' },
  type: { width: 160, sort: 'type' },
  mtime: { width: 140, sort: 'mtime' },
  note: { width: 200, sort: 'note' },
  btime: { width: 140, sort: 'btime' },
  atime: { width: 140, sort: 'atime' },
  ext: { width: 80, sort: 'ext' },
  mode: { width: 150, sort: 'mode', mono: true },
  itemCount: { width: 96, align: 'right' },
  dimensions: { width: 110, sort: 'dimensions' },
  duration: { width: 90, align: 'right', sort: 'duration' },
  shared: { width: 110 },
  location: { width: 240, mono: true },
}

/** `view.columns` preference (SPEC §8.18). */
export interface ColumnsPref {
  visible: ColumnId[]
  order: ColumnId[]
  widths: Partial<Record<ColumnId, number>>
}

export const DEFAULT_COLUMNS_PREF: ColumnsPref = {
  visible: DEFAULT_COLUMNS,
  order: ALL_COLUMNS,
  widths: {},
}

export function isColumnId(v: unknown): v is ColumnId {
  return typeof v === 'string' && (ALL_COLUMNS as string[]).includes(v)
}

export function normalizeColumns(v: unknown): ColumnsPref {
  if (!v || typeof v !== 'object') return DEFAULT_COLUMNS_PREF
  const o = v as Record<string, unknown>
  const visible = Array.isArray(o.visible) ? o.visible.filter(isColumnId) : DEFAULT_COLUMNS
  if (!visible.includes('name')) visible.unshift('name')
  const order = Array.isArray(o.order) ? o.order.filter(isColumnId) : []
  for (const id of ALL_COLUMNS) if (!order.includes(id)) order.push(id)
  const widths: Partial<Record<ColumnId, number>> = {}
  if (o.widths && typeof o.widths === 'object') {
    for (const [k, w] of Object.entries(o.widths as Record<string, unknown>)) {
      if (isColumnId(k) && typeof w === 'number' && w >= 40 && w <= 2000) widths[k] = Math.round(w)
    }
  }
  return { visible: [...new Set(visible)], order: [...new Set(order)], widths }
}

/** Visible columns in display order (name always first). */
export function orderedVisible(pref: ColumnsPref, allowContext = false): ColumnId[] {
  const out = pref.order.filter(
    (id) => pref.visible.includes(id) && (allowContext || !CONTEXT_COLUMNS.includes(id)),
  )
  const i = out.indexOf('name')
  if (i > 0) {
    out.splice(i, 1)
    out.unshift('name')
  }
  return out
}

export function columnWidth(pref: ColumnsPref, id: ColumnId): number {
  return pref.widths[id] ?? COLUMN_META[id].width
}

/** Column definition rendered directly by DetailsView (no TanStack Table: its row
 *  model rebuilt 10.000 Row objects on every page append, which was the largest
 *  long task in the 10k E2E; TanStack Virtual is still used for the rows). */
export interface ColumnDef {
  id: ColumnId
  header: () => string
  cell: (item: Item) => ReactNode
  meta: ColumnMeta
}

/** Loose `t` for dynamic keys coming from the API (`typeLabel`). */
type LooseT = (key: string, options?: Record<string, unknown>) => string

export function typeLabel(t: TFunction<'files'>, item: Item): string {
  const tr = t as unknown as LooseT
  if (item.isSymlink && item.broken) return tr('type.symlink')
  const msg = tr(item.typeLabel, { defaultValue: '' })
  if (msg) return msg
  return tr('type.fallback', { ext: (item.ext || item.name.split('.').pop() || '').toUpperCase() })
}

const DASH = '—'

function dateCell(value: string | null | undefined, lang: string) {
  if (!value) return DASH
  return (
    <span title={formatDateTimeFull(value, lang)} className="tabular">
      {formatRelativeTime(value, lang)}
    </span>
  )
}

export function buildColumns(t: TFunction<'files'>, lang: string): ColumnDef[] {
  return [
    {
      id: 'name',
      header: () => t('columns.name'),
      meta: COLUMN_META.name,
      cell: (it) => {
        return (
          <span className="flex min-w-0 items-center gap-2" data-name>
            <FileIcon category={it.isDir ? 'folder' : toIconCategory(it.category)} size={16} />
            <span className="truncate" title={it.name}>
              {it.name}
            </span>
            {it.broken ? (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Link2Off
                    className="size-3.5 shrink-0 text-danger"
                    aria-label={t('grid.broken')}
                  />
                </TooltipTrigger>
                <TooltipContent>{t('grid.broken')}</TooltipContent>
              </Tooltip>
            ) : null}
          </span>
        )
      },
    },
    {
      id: 'size',
      header: () => t('columns.size'),
      meta: COLUMN_META.size,
      cell: (it) => {
        if (it.isDir) {
          return it.itemCount === null || it.itemCount === undefined
            ? DASH
            : t('grid.folderItems', { count: it.itemCount, n: formatNumber(it.itemCount, lang) })
        }
        return formatBytes(it.size, lang)
      },
    },
    {
      id: 'type',
      header: () => t('columns.type'),
      meta: COLUMN_META.type,
      cell: (it) => <span className="truncate">{typeLabel(t, it)}</span>,
    },
    {
      id: 'mtime',
      header: () => t('columns.mtime'),
      meta: COLUMN_META.mtime,
      cell: (it) => dateCell(it.mtime, lang),
    },
    {
      id: 'note',
      header: () => t('columns.note'),
      meta: COLUMN_META.note,
      cell: (it) => <span className="truncate text-text-muted">{it.note ?? ''}</span>,
    },
    {
      id: 'btime',
      header: () => t('columns.btime'),
      meta: COLUMN_META.btime,
      cell: (it) => dateCell(it.btime, lang),
    },
    {
      id: 'atime',
      header: () => t('columns.atime'),
      meta: COLUMN_META.atime,
      cell: (it) => dateCell(it.atime, lang),
    },
    {
      id: 'ext',
      header: () => t('columns.ext'),
      meta: COLUMN_META.ext,
      cell: (it) => (it.isDir ? DASH : it.ext || DASH),
    },
    {
      id: 'mode',
      header: () => t('columns.mode'),
      meta: COLUMN_META.mode,
      cell: (it) => (
        <span className="font-mono text-xs">
          {it.mode} {it.modeOctal}
        </span>
      ),
    },
    {
      id: 'itemCount',
      header: () => t('columns.itemCount'),
      meta: COLUMN_META.itemCount,
      cell: (it) => {
        if (!it.isDir) return DASH
        return it.itemCount === null || it.itemCount === undefined
          ? DASH
          : formatNumber(it.itemCount, lang)
      },
    },
    {
      id: 'dimensions',
      header: () => t('columns.dimensions'),
      meta: COLUMN_META.dimensions,
      cell: (it) => {
        const m = it.media
        return m?.width && m.height ? `${m.width} × ${m.height}` : DASH
      },
    },
    {
      id: 'duration',
      header: () => t('columns.duration'),
      meta: COLUMN_META.duration,
      cell: (it) => {
        const d = it.media?.durationMs
        return d ? formatDuration(d / 1000) : DASH
      },
    },
    {
      id: 'shared',
      header: () => t('columns.shared'),
      meta: COLUMN_META.shared,
      cell: (it) => t(`shared.${it.shared}` as 'shared.none'),
    },
    {
      id: 'location',
      header: () => t('columns.location'),
      meta: COLUMN_META.location,
      cell: (it) => {
        const p = it.path
        const i = p.lastIndexOf('/')
        return <span className="font-mono text-xs">{i <= 0 ? '/' : p.slice(0, i)}</span>
      },
    },
  ]
}
