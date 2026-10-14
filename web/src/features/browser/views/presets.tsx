import { Link2Off } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatBytes, formatRelativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { Item } from '../api'
import { typeLabel } from '../columns'
import { GridView, type GridViewProps } from './GridView'
import { Thumb } from './Thumb'
import { LARGE_SPEC, TILES_SPEC, XLARGE_SPEC, middleEllipsis } from './gridMath'

type PresetProps = Omit<GridViewProps, 'spec' | 'renderCell' | 'view'>

function Broken({ item }: { item: Item }) {
  const { t } = useTranslation('files')
  if (!item.broken) return null
  return <Link2Off className="size-3.5 shrink-0 text-danger" aria-label={t('grid.broken')} />
}

function TileCell({ item }: { item: Item }) {
  const { t, i18n } = useTranslation('files')
  const lang = i18n.resolvedLanguage ?? 'id'
  return (
    <div className="flex h-full items-center gap-2 px-2">
      <Thumb item={item} box={48} iconSize={28} className="shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1">
          <span className="truncate font-medium" title={item.name}>
            {item.name}
          </span>
          <Broken item={item} />
        </div>
        <div className="truncate text-xs text-text-muted">{typeLabel(t, item)}</div>
        <div className="truncate text-xs text-text-muted tabular">
          {item.isDir ? formatRelativeTime(item.mtime, lang) : formatBytes(item.size, lang)}
        </div>
      </div>
    </div>
  )
}

function IconCell({ item, box, chars }: { item: Item; box: number; chars: number }) {
  return (
    <div className="flex h-full flex-col items-center gap-1 p-2">
      <Thumb item={item} box={box} className="shrink-0" />
      <div
        className={cn(
          'flex w-full items-center justify-center gap-1 px-1 text-center text-xs leading-4',
        )}
      >
        <span className="line-clamp-2 break-all" title={item.name}>
          {middleEllipsis(item.name, chars)}
        </span>
        <Broken item={item} />
      </div>
    </div>
  )
}

export function TilesView(props: PresetProps) {
  return (
    <GridView
      {...props}
      spec={TILES_SPEC}
      view="tiles"
      renderCell={(item) => <TileCell item={item} />}
    />
  )
}

export function LargeIconsView(props: PresetProps) {
  return (
    <GridView
      {...props}
      spec={LARGE_SPEC}
      view="large"
      renderCell={(item) => <IconCell item={item} box={128} chars={34} />}
    />
  )
}

export function XLIconsView(props: PresetProps) {
  return (
    <GridView
      {...props}
      spec={XLARGE_SPEC}
      view="xlarge"
      renderCell={(item) => <IconCell item={item} box={256} chars={60} />}
    />
  )
}
