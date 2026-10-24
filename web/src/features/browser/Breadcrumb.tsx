import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ChevronDown, ChevronRight, Folder, MoreHorizontal } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Skeleton } from '@/components/ui/skeleton'
import { dropTargetProps } from '@/features/file-ops/dnd'
import { cn } from '@/lib/utils'
import { listDir, listQueryKey } from './api'
import { pathToSplat, ROOT } from './path'
import { collapseSegments, segmentsOf, type Segment } from './breadcrumb-utils'

function FolderLink({
  path,
  children,
  className,
}: {
  path: string
  children: React.ReactNode
  className?: string
}) {
  const [over, setOver] = useState(false)
  return (
    <Link
      to="/files/$"
      params={{ _splat: pathToSplat(path) }}
      className={cn(
        'max-w-48 truncate rounded px-1.5 py-0.5 text-sm text-text hover:bg-surface-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus',
        over && 'bg-accent/15 outline outline-2 -outline-offset-1 outline-accent',
        className,
      )}
      data-drop-over={over || undefined}
      data-testid={`crumb-${path}`}
      {...dropTargetProps({ dir: path, onOver: (d) => setOver(d !== null) })}
    >
      {children}
    </Link>
  )
}

/** Dropdown listing sibling folders of `segment` (children for the root). Lazy: fetched on open. */
function SiblingMenu({ segment, parent }: { segment: Segment; parent: string }) {
  const { t } = useTranslation('files')
  const [open, setOpen] = useState(false)
  const q = useQuery({
    queryKey: [...listQueryKey(parent, { types: 'folder' }), 'siblings'],
    queryFn: ({ signal }) => listDir(parent, { types: 'folder', limit: 200 }, null, signal),
    enabled: open,
    staleTime: 15_000,
  })
  const label =
    segment.path === ROOT
      ? t('nav.children')
      : t('nav.siblings', { name: segment.name || t('nav.root') })
  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={label}
          data-testid={`crumb-menu-${segment.path}`}
          className="grid min-h-6 min-w-6 place-items-center rounded text-text-muted hover:bg-surface-2 hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
        >
          <ChevronDown className="size-3.5" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-56 overflow-auto">
        <DropdownMenuLabel className="truncate text-xs text-text-muted">{label}</DropdownMenuLabel>
        {q.isPending ? (
          <div className="space-y-1 p-2">
            <Skeleton className="h-4 w-3/4" />
            <Skeleton className="h-4 w-1/2" />
          </div>
        ) : q.isError || !q.data || q.data.items.length === 0 ? (
          <DropdownMenuItem disabled>{t('grid.items', { count: 0, n: '0' })}</DropdownMenuItem>
        ) : (
          q.data.items.map((it) => (
            <DropdownMenuItem
              key={it.path}
              asChild
              className={cn(it.path === segment.path && 'bg-accent-subtle')}
            >
              <Link to="/files/$" params={{ _splat: pathToSplat(it.path) }}>
                <Folder className="size-4 text-icon-folder" aria-hidden="true" />
                <span className="truncate">{it.name}</span>
              </Link>
            </DropdownMenuItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function parentOf(segments: Segment[], i: number): string {
  return i === 0 ? ROOT : segments[i - 1]!.path
}

export interface BreadcrumbProps {
  path: string
  /** Click on the empty area switches the address bar into typing mode. */
  onEditRequest?: () => void
  compact?: boolean
  className?: string
}

export function Breadcrumb({ path, onEditRequest, compact = false, className }: BreadcrumbProps) {
  const { t } = useTranslation('files')
  const segments = segmentsOf(path)
  const last = segments[segments.length - 1]!
  if (compact) {
    return (
      <nav aria-label={t('nav.breadcrumb')} className={cn('flex min-w-0 items-center', className)}>
        <span aria-current="page" className="truncate px-1.5 text-sm font-medium text-text">
          {last.path === ROOT ? t('nav.root') : last.name}
        </span>
      </nav>
    )
  }
  const { head, hidden, tail } = collapseSegments(segments)
  const render = (seg: Segment, i: number, isLast: boolean) => (
    <li key={seg.path} className="flex min-w-0 items-center">
      {i > 0 ? (
        <ChevronRight className="size-3.5 shrink-0 text-text-muted" aria-hidden="true" />
      ) : null}
      {isLast ? (
        <span
          aria-current="page"
          className="max-w-48 truncate px-1.5 text-sm font-medium text-text"
        >
          {seg.path === ROOT ? t('nav.root') : seg.name}
        </span>
      ) : (
        <FolderLink path={seg.path}>{seg.path === ROOT ? t('nav.root') : seg.name}</FolderLink>
      )}
      <SiblingMenu segment={seg} parent={parentOf(segments, i)} />
    </li>
  )
  const items: React.ReactNode[] = []
  head.forEach((seg, i) =>
    items.push(
      render(seg, i, hidden.length === 0 && tail.length === 0 && i === segments.length - 1),
    ),
  )
  if (hidden.length > 0) {
    items.push(
      <li key="ellipsis" className="flex items-center">
        <ChevronRight className="size-3.5 shrink-0 text-text-muted" aria-hidden="true" />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              aria-label={t('nav.hiddenSegments')}
              data-testid="crumb-ellipsis"
              className="rounded px-1 py-0.5 text-text-muted hover:bg-surface-2 hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus"
            >
              <MoreHorizontal className="size-4" aria-hidden="true" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-56">
            {hidden.map((seg) => (
              <DropdownMenuItem key={seg.path} asChild>
                <Link to="/files/$" params={{ _splat: pathToSplat(seg.path) }}>
                  <Folder className="size-4 text-icon-folder" aria-hidden="true" />
                  <span className="truncate">{seg.name}</span>
                </Link>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </li>,
    )
    tail.forEach((seg, j) => items.push(render(seg, segments.indexOf(seg), j === tail.length - 1)))
  }
  return (
    <nav
      aria-label={t('nav.breadcrumb')}
      className={cn('flex min-w-0 flex-1 items-center', className)}
      onClick={(e) => {
        if (e.target === e.currentTarget) onEditRequest?.()
      }}
      data-testid="breadcrumb"
    >
      <ol className="flex min-w-0 items-center gap-0.5">{items}</ol>
      <button
        type="button"
        aria-label={t('nav.editPath')}
        title={t('nav.editPath')}
        onClick={onEditRequest}
        data-testid="breadcrumb-edit"
        className="h-full min-w-6 flex-1 cursor-text self-stretch rounded"
      />
    </nav>
  )
}
