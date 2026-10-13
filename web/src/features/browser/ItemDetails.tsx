import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { formatBytes, formatDateTimeFull, formatDuration, formatNumber } from '@/lib/format'
import type { Item } from './api'
import { typeLabel } from './columns'

export function ItemHeader({ item, size = 48 }: { item: Item; size?: number }) {
  const { t } = useTranslation('files')
  return (
    <div className="flex min-w-0 items-center gap-3">
      <FileIcon
        category={item.isDir ? 'folder' : toIconCategory(item.category)}
        size={size}
        className="shrink-0"
      />
      <div className="min-w-0">
        <div className="truncate font-medium text-text" title={item.name}>
          {item.name}
        </div>
        <div className="truncate text-xs text-text-muted">{typeLabel(t, item)}</div>
      </div>
    </div>
  )
}

const DASH = '—'

/** Key/value list of item metadata (SPEC §9.3 preview panel / Properties). */
export function ItemFacts({
  item,
  stat,
  versionsCount,
}: {
  item: Item
  stat?: { ctime: string; nlink: number } | undefined
  versionsCount?: number | undefined
}) {
  const { t, i18n } = useTranslation('files')
  const lang = i18n.resolvedLanguage ?? 'id'
  const rows: [string, string][] = [
    [t('props.path'), item.path],
    [t('props.type'), typeLabel(t, item)],
    [
      t('props.size'),
      item.isDir
        ? item.itemCount === null || item.itemCount === undefined
          ? DASH
          : t('grid.folderItems', { count: item.itemCount, n: formatNumber(item.itemCount, lang) })
        : formatBytes(item.size, lang),
    ],
    [t('props.mtime'), formatDateTimeFull(item.mtime, lang)],
    [t('props.btime'), item.btime ? formatDateTimeFull(item.btime, lang) : DASH],
    [t('props.atime'), formatDateTimeFull(item.atime, lang)],
    [t('props.ctime'), stat ? formatDateTimeFull(stat.ctime, lang) : DASH],
    [t('props.mode'), `${item.mode} ${item.modeOctal}`],
  ]
  if (item.mime) rows.push([t('props.mime'), item.mime])
  if (item.media?.width && item.media.height)
    rows.push([t('columns.dimensions'), `${item.media.width} × ${item.media.height}`])
  if (item.media?.duration) rows.push([t('columns.duration'), formatDuration(item.media.duration)])
  if (item.isSymlink) rows.push([t('props.target'), item.symlinkTarget ?? DASH])
  if (item.note) rows.push([t('columns.note'), item.note])
  rows.push([t('columns.shared'), t(`shared.${item.shared}` as 'shared.none')])
  if (versionsCount !== undefined)
    rows.push([t('props.versions'), formatNumber(versionsCount, lang)])
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs" data-testid="item-facts">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-text-muted">{k}</dt>
          <dd
            className={
              k === t('props.path') || k === t('props.mode') ? 'font-mono break-all' : 'break-words'
            }
          >
            {v}
          </dd>
        </div>
      ))}
    </dl>
  )
}
