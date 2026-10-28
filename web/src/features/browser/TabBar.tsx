import { Plus, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import { IconButton } from '@/components/ui/icon-button'
import { cn } from '@/lib/utils'
import { MAX_TABS, type Tab } from '@/stores/tabs'
import { baseName, ROOT } from './path'

export interface TabBarProps {
  tabs: Tab[]
  activeId: string
  onActivate: (id: string) => void
  onClose: (id: string) => void
  onCloseOthers: (id: string) => void
  onCloseRight: (id: string) => void
  onNew: () => void
  onReorder: (id: string, toIndex: number) => void
  /** Dual-pane (Step 23b): accessible name of the tablist (unique per pane). */
  label?: string | undefined
}

/** Folder tabs below the address bar (SPEC §9.3 / PROMPT 7b task 7). */
export function TabBar({
  tabs,
  activeId,
  onActivate,
  onClose,
  onCloseOthers,
  onCloseRight,
  onNew,
  onReorder,
  label: listLabel,
}: TabBarProps) {
  const { t } = useTranslation('files')
  const [dragId, setDragId] = useState<string | null>(null)
  const [over, setOver] = useState<string | null>(null)
  const label = (tab: Tab) => (tab.path === ROOT ? t('nav.root') : baseName(tab.path))
  return (
    <div
      className="flex h-9 shrink-0 items-end gap-0.5 border-b border-border bg-surface px-2"
      data-testid="tab-bar"
    >
      <div
        className="flex min-w-0 items-end gap-0.5"
        role="tablist"
        aria-label={listLabel ?? t('tabs.label')}
      >
        {tabs.map((tab, i) => {
          const active = tab.id === activeId
          return (
            <ContextMenu key={tab.id}>
              <ContextMenuTrigger asChild>
                <div
                  role="tab"
                  aria-selected={active}
                  aria-controls="main"
                  tabIndex={active ? 0 : -1}
                  title={tab.path}
                  draggable
                  onDragStart={(e) => {
                    setDragId(tab.id)
                    e.dataTransfer.setData('text/x-tab', tab.id)
                    e.dataTransfer.effectAllowed = 'move'
                  }}
                  onDragOver={(e) => {
                    e.preventDefault()
                    setOver(tab.id)
                  }}
                  onDragLeave={() => setOver((o) => (o === tab.id ? null : o))}
                  onDrop={(e) => {
                    e.preventDefault()
                    const id = e.dataTransfer.getData('text/x-tab') || dragId
                    setOver(null)
                    setDragId(null)
                    if (id && id !== tab.id) onReorder(id, i)
                  }}
                  onDragEnd={() => {
                    setDragId(null)
                    setOver(null)
                  }}
                  onClick={() => onActivate(tab.id)}
                  onAuxClick={(e) => {
                    if (e.button === 1) onClose(tab.id)
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      onActivate(tab.id)
                    } else if (e.key === 'Delete') {
                      e.preventDefault()
                      onClose(tab.id)
                    } else if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
                      e.preventDefault()
                      const next =
                        tabs[(i + (e.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length]
                      if (next) onActivate(next.id)
                    }
                  }}
                  className={cn(
                    'group flex h-8 max-w-48 min-w-0 cursor-default items-center gap-1 rounded-t-md border border-b-0 px-2 text-xs select-none',
                    active
                      ? 'border-border bg-bg text-text'
                      : 'border-transparent text-text-muted hover:bg-surface-2 hover:text-text',
                    over === tab.id && dragId !== tab.id && 'ring-2 ring-accent/60',
                  )}
                  data-testid={`tab-${i}`}
                  data-active={active || undefined}
                >
                  <span className="truncate">{label(tab)}</span>
                  <span
                    aria-hidden="true"
                    title={t('tabs.close', { name: label(tab) })}
                    className="ml-auto rounded-sm p-0.5 opacity-0 group-hover:opacity-100 hover:bg-border group-aria-selected:opacity-100"
                    onClick={(e) => {
                      e.stopPropagation()
                      onClose(tab.id)
                    }}
                    data-testid={`tab-close-${i}`}
                  >
                    <X className="size-3" />
                  </span>
                </div>
              </ContextMenuTrigger>
              <ContextMenuContent className="w-48">
                <ContextMenuItem onSelect={() => onClose(tab.id)}>
                  {t('tabs.menuClose')}
                </ContextMenuItem>
                <ContextMenuItem
                  onSelect={() => onCloseOthers(tab.id)}
                  disabled={tabs.length === 1}
                >
                  {t('tabs.closeOthers')}
                </ContextMenuItem>
                <ContextMenuItem
                  onSelect={() => onCloseRight(tab.id)}
                  disabled={i === tabs.length - 1}
                >
                  {t('tabs.closeRight')}
                </ContextMenuItem>
                <ContextMenuSeparator />
                <ContextMenuItem onSelect={onNew} disabled={tabs.length >= MAX_TABS}>
                  {t('tabs.new')}
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenu>
          )
        })}
      </div>
      <IconButton
        label={t('tabs.new')}
        shortcut="Alt+T"
        size="icon-sm"
        className="mb-0.5"
        onClick={onNew}
        data-testid="tab-new"
      >
        <Plus aria-hidden="true" />
      </IconButton>
    </div>
  )
}
