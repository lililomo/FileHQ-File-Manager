import { createColumnHelper, createCoreRowModel, tableFeatures } from '@tanstack/react-table'
import type { TFunction } from 'i18next'
import { Link2Off } from 'lucide-react'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { formatBytes, formatDateTimeFull, formatNumber, formatRelativeTime } from '@/lib/format'
import type { Item } from './api'

export type ColumnId = 'name' | 'size' | 'type' | 'mtime' | 'note'

export const DEFAULT_COLUMNS: ColumnId[] = ['name', 'size', 'type', 'mtime', 'note']
export const MOBILE_COLUMNS: ColumnId[] = ['name', 'mtime']

export interface ColumnMeta {
  width: number
  align?: 'left' | 'right'
  grow?: boolean
}

/** TanStack Table v9 feature set for the Details view (sorting/visibility/sizing are added in 7b). */
export const tableFeatureSet = tableFeatures({
  coreRowModel: createCoreRowModel(),
  columnMeta: {} as ColumnMeta,
})
export type TableFeatureSet = typeof tableFeatureSet

const col = createColumnHelper<TableFeatureSet, Item>()

/** Loose `t` for dynamic keys coming from the API (`typeLabel`). */
type LooseT = (key: string, options?: Record<string, unknown>) => string

export function typeLabel(t: TFunction<'files'>, item: Item): string {
  const tr = t as unknown as LooseT
  if (item.isSymlink && item.broken) return tr('type.symlink')
  const msg = tr(item.typeLabel, { defaultValue: '' })
  if (msg) return msg
  return tr('type.fallback', { ext: (item.ext || item.name.split('.').pop() || '').toUpperCase() })
}

export function buildColumns(t: TFunction<'files'>, lang: string) {
  return col.columns([
    col.accessor('name', {
      id: 'name',
      header: () => t('columns.name'),
      meta: { width: 320, grow: true },
      cell: ({ row }) => {
        const it = row.original
        return (
          <span className="flex min-w-0 items-center gap-2">
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
    }),
    col.accessor('size', {
      id: 'size',
      header: () => t('columns.size'),
      meta: { width: 96, align: 'right' },
      cell: ({ row }) => {
        const it = row.original
        if (it.isDir) {
          return it.itemCount === null || it.itemCount === undefined
            ? '—'
            : t('grid.folderItems', { count: it.itemCount, n: formatNumber(it.itemCount, lang) })
        }
        return formatBytes(it.size, lang)
      },
    }),
    col.accessor('typeLabel', {
      id: 'type',
      header: () => t('columns.type'),
      meta: { width: 160 },
      cell: ({ row }) => <span className="truncate">{typeLabel(t, row.original)}</span>,
    }),
    col.accessor('mtime', {
      id: 'mtime',
      header: () => t('columns.mtime'),
      meta: { width: 140 },
      cell: ({ row }) => (
        <span title={formatDateTimeFull(row.original.mtime, lang)} className="tabular">
          {formatRelativeTime(row.original.mtime, lang)}
        </span>
      ),
    }),
    col.accessor('note', {
      id: 'note',
      header: () => t('columns.note'),
      meta: { width: 200 },
      cell: ({ row }) => (
        <span className="truncate text-text-muted">{row.original.note ?? ''}</span>
      ),
    }),
  ])
}
