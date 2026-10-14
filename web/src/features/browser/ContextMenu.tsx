import { Folder } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import { menuItems, type ActionContext, type ActionDef, type MenuContext } from '@/lib/actions'
import { actionLabel, actionShortcut, type LooseT } from './actionLabel'

export interface BrowserContextMenuProps {
  ctx: ActionContext | null
  /** Which SPEC §9.5 context the menu is for (decided before it opens). */
  context: MenuContext
  /** Submenu bodies supplied by the browser screen (view / sort / columns). */
  viewMenu: ReactNode
  sortMenu: ReactNode
  columnsMenu: ReactNode
  templates: string[]
  onOpenChange?: (open: boolean) => void
  children: ReactNode
}

/** Right-click menu of the file grid (SPEC §9.5): one Radix ContextMenu around
 *  the view; the items come from the action registry's matrix for the
 *  context, unavailable/inapplicable actions are omitted (not disabled). */
export function BrowserContextMenu({
  ctx,
  context,
  viewMenu,
  sortMenu,
  columnsMenu,
  templates,
  onOpenChange,
  children,
}: BrowserContextMenuProps) {
  const { t } = useTranslation(['files', 'fileops'])
  const lt = t as unknown as LooseT
  const groups = ctx ? menuItems(context, ctx) : []

  const renderSubmenu = (a: ActionDef) => {
    const Icon = a.icon
    let body: ReactNode
    switch (a.id) {
      case 'new':
        body = (
          <>
            <ContextMenuItem onSelect={() => ctx?.openNew('file')} data-testid="cm-newFile">
              {t('files:actions.newFile')}
            </ContextMenuItem>
            <ContextMenuItem onSelect={() => ctx?.openNew('folder')} data-testid="cm-newFolder">
              {t('files:actions.newFolder')}
            </ContextMenuItem>
            <ContextMenuSub>
              <ContextMenuSubTrigger data-testid="cm-newFromTemplate">
                {t('files:actions.newFromTemplate')}
              </ContextMenuSubTrigger>
              <ContextMenuSubContent className="max-h-72 overflow-auto">
                {templates.length === 0 ? (
                  <ContextMenuItem onSelect={() => ctx?.openNew('template')}>
                    {t('fileops:new.chooseTemplate')}
                  </ContextMenuItem>
                ) : (
                  templates.map((name) => (
                    <ContextMenuItem
                      key={name}
                      onSelect={() => ctx?.openNew('template', { template: name })}
                      data-testid={`cm-template-${name}`}
                    >
                      {`.${name}`}
                    </ContextMenuItem>
                  ))
                )}
              </ContextMenuSubContent>
            </ContextMenuSub>
            <ContextMenuItem onSelect={() => ctx?.openNew('symlink')} data-testid="cm-newSymlink">
              {t('files:actions.newSymlink')}
            </ContextMenuItem>
          </>
        )
        break
      case 'view':
        body = viewMenu
        break
      case 'sort':
        body = sortMenu
        break
      case 'columns':
        body = columnsMenu
        break
      default:
        return null
    }
    return (
      <ContextMenuSub key={a.id}>
        <ContextMenuSubTrigger data-testid={`cm-${a.id}`} data-action={a.id}>
          <Icon aria-hidden="true" />
          {actionLabel(lt, a)}
        </ContextMenuSubTrigger>
        <ContextMenuSubContent className="w-56">{body}</ContextMenuSubContent>
      </ContextMenuSub>
    )
  }

  return (
    <ContextMenu onOpenChange={onOpenChange} modal={false}>
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent className="w-64" data-testid="context-menu" data-context={context}>
        {groups.length === 0 ? (
          <ContextMenuItem disabled>
            <Folder aria-hidden="true" />
            {t('fileops:menu.empty')}
          </ContextMenuItem>
        ) : null}
        {groups.map((group, gi) => (
          <div key={gi} role="group">
            {gi > 0 ? <ContextMenuSeparator /> : null}
            {group.map((a) => {
              if (a.submenu) return renderSubmenu(a)
              const Icon = a.icon
              const keys = actionShortcut(a)
              return (
                <ContextMenuItem
                  key={a.id}
                  variant={a.destructive ? 'destructive' : 'default'}
                  onSelect={() => ctx && a.run(ctx)}
                  data-testid={`cm-${a.id}`}
                  data-action={a.id}
                >
                  <Icon aria-hidden="true" />
                  {actionLabel(lt, a)}
                  {keys ? <ContextMenuShortcut>{keys}</ContextMenuShortcut> : null}
                </ContextMenuItem>
              )
            })}
          </div>
        ))}
      </ContextMenuContent>
    </ContextMenu>
  )
}
