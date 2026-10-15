import {
  useCallback,
  useRef,
  useState,
  type DragEvent,
  type KeyboardEvent,
  type MouseEvent,
} from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { FileIcon } from '@/components/file-icon/FileIcon'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import { cn } from '@/lib/utils'
import type { EditorTab } from '@/stores/editor'

export interface TabBarProps {
  tabs: EditorTab[]
  activePath: string | null
  onActivate: (path: string) => void
  onClose: (path: string) => void
  onCloseOthers: (path: string) => void
  onCloseRight: (path: string) => void
  onCloseAll: () => void
  onReorder: (from: number, to: number) => void
  onCopyPath: (path: string) => void
  onReveal: (path: string) => void
}

/** Editor tabs (SPEC §8.13 "Tab & sesi"): ● for unsaved, close on the ✕ /
 *  middle click / Alt+W, context menu, HTML5 drag reorder, roving tabindex. */
export function TabBar(p: TabBarProps) {
  const { t } = useTranslation('editor')
  const [dragFrom, setDragFrom] = useState<number | null>(null)
  const [dropAt, setDropAt] = useState<number | null>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const onKey = useCallback(
    (e: KeyboardEvent<HTMLDivElement>, i: number) => {
      const tab = p.tabs[i]
      if (!tab) return
      let next: number | null = null
      if (e.key === 'ArrowRight') next = (i + 1) % p.tabs.length
      else if (e.key === 'ArrowLeft') next = (i - 1 + p.tabs.length) % p.tabs.length
      else if (e.key === 'Home') next = 0
      else if (e.key === 'End') next = p.tabs.length - 1
      else if (e.key === 'Delete') {
        e.preventDefault()
        p.onClose(tab.path)
        return
      }
      if (next === null) return
      e.preventDefault()
      p.onActivate(p.tabs[next]!.path)
      const el = listRef.current?.querySelectorAll<HTMLElement>('[role=tab]')[next]
      el?.focus()
    },
    [p],
  )

  const onDragStart = (e: DragEvent, i: number) => {
    setDragFrom(i)
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', p.tabs[i]!.path)
  }
  const onDragOver = (e: DragEvent, i: number) => {
    if (dragFrom === null) return
    e.preventDefault()
    setDropAt(i)
  }
  const onDrop = (e: DragEvent, i: number) => {
    e.preventDefault()
    if (dragFrom !== null && dragFrom !== i) p.onReorder(dragFrom, i)
    setDragFrom(null)
    setDropAt(null)
  }

  return (
    <div
      ref={listRef}
      role="tablist"
      aria-label={t('tabs.label')}
      data-testid="editor-tabs"
      className="flex h-9 shrink-0 items-stretch overflow-x-auto border-b border-border bg-surface-2 text-sm"
    >
      {p.tabs.map((tab, i) => {
        const active = tab.path === p.activePath
        return (
          <ContextMenu key={tab.path}>
            <ContextMenuTrigger asChild>
              <div
                role="tab"
                aria-selected={active}
                tabIndex={active ? 0 : -1}
                data-testid="editor-tab"
                data-path={tab.path}
                data-dirty={tab.dirty || undefined}
                draggable
                title={tab.path}
                onClick={() => p.onActivate(tab.path)}
                onAuxClick={(e: MouseEvent) => {
                  if (e.button === 1) {
                    e.preventDefault()
                    p.onClose(tab.path)
                  }
                }}
                onKeyDown={(e) => onKey(e, i)}
                onDragStart={(e) => onDragStart(e, i)}
                onDragOver={(e) => onDragOver(e, i)}
                onDragLeave={() => setDropAt((d) => (d === i ? null : d))}
                onDrop={(e) => onDrop(e, i)}
                onDragEnd={() => {
                  setDragFrom(null)
                  setDropAt(null)
                }}
                className={cn(
                  'group flex max-w-56 min-w-0 cursor-pointer items-center gap-1.5 border-r border-border px-3 outline-none select-none focus-visible:ring-2 focus-visible:ring-focus focus-visible:ring-inset',
                  active
                    ? 'bg-surface text-text'
                    : 'text-text-muted hover:bg-surface hover:text-text',
                  dropAt === i && dragFrom !== i && 'ring-2 ring-accent ring-inset',
                )}
              >
                <FileIcon
                  category={tab.status === 'binary' ? 'other' : 'code'}
                  size={14}
                  className="shrink-0"
                />
                <span className="truncate">{tab.name}</span>
                {tab.dirty ? (
                  <span
                    className="text-accent"
                    aria-label={t('tabs.unsaved')}
                    data-testid="tab-dirty"
                  >
                    {'\u25CF'}
                  </span>
                ) : null}
                <span
                  role="presentation"
                  aria-hidden
                  data-testid="tab-close"
                  onClick={(e) => {
                    e.stopPropagation()
                    p.onClose(tab.path)
                  }}
                  className={cn(
                    'ml-auto rounded p-0.5 hover:bg-surface-3',
                    active ? 'opacity-70' : 'opacity-0 group-hover:opacity-70',
                  )}
                >
                  <X className="size-3.5" />
                </span>
              </div>
            </ContextMenuTrigger>
            <ContextMenuContent>
              <ContextMenuItem onSelect={() => p.onClose(tab.path)}>
                {t('tabs.close')}
              </ContextMenuItem>
              <ContextMenuItem onSelect={() => p.onCloseOthers(tab.path)}>
                {t('tabs.closeOthers')}
              </ContextMenuItem>
              <ContextMenuItem onSelect={() => p.onCloseRight(tab.path)}>
                {t('tabs.closeRight')}
              </ContextMenuItem>
              <ContextMenuItem onSelect={() => p.onCloseAll()}>
                {t('tabs.closeAll')}
              </ContextMenuItem>
              <ContextMenuSeparator />
              <ContextMenuItem onSelect={() => p.onCopyPath(tab.path)}>
                {t('tabs.copyPath')}
              </ContextMenuItem>
              <ContextMenuItem onSelect={() => p.onReveal(tab.path)}>
                {t('tabs.reveal')}
              </ContextMenuItem>
            </ContextMenuContent>
          </ContextMenu>
        )
      })}
    </div>
  )
}
