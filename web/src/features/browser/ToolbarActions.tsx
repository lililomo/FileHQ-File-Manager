import { MoreHorizontal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import {
  actionById,
  isAvailable,
  TOOLBAR_MORE,
  TOOLBAR_PRIMARY,
  type ActionContext,
} from '@/lib/actions'
import { actionLabel, actionShortcut } from './actionLabel'

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** Contextual toolbar (PROMPT 9b task 2): the primary file operations as icon
 *  buttons (disabled when not applicable) and the remaining applicable
 *  actions under "⋯". */
export function ToolbarActions({
  ctx,
  compact = false,
}: {
  ctx: ActionContext | null
  compact?: boolean
}) {
  const { t } = useTranslation(['files'])
  const lt = t as unknown as LooseT
  const primary = compact ? [] : TOOLBAR_PRIMARY
  const moreIds = compact ? [...TOOLBAR_PRIMARY, ...TOOLBAR_MORE] : TOOLBAR_MORE
  const more = moreIds
    .map((id) => actionById(id))
    .filter(
      (a): a is NonNullable<typeof a> =>
        !!a && isAvailable(a) && (ctx ? a.isApplicable(ctx) : false),
    )
  return (
    <>
      {primary.map((id) => {
        const a = actionById(id)
        if (!a || !isAvailable(a)) return null
        const Icon = a.icon
        const enabled = !!ctx && a.isApplicable(ctx)
        const keys = actionShortcut(a)
        return (
          <IconButton
            key={id}
            label={actionLabel(lt, a)}
            {...(keys ? { shortcut: keys } : {})}
            disabled={!enabled}
            onClick={() => ctx && a.run(ctx)}
            className={a.destructive && enabled ? 'hover:text-danger' : undefined}
            data-testid={`toolbar-${id}`}
          >
            <Icon aria-hidden="true" />
          </IconButton>
        )
      })}
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <IconButton
            label={t('files:toolbar.more')}
            disabled={more.length === 0}
            data-testid="toolbar-more"
          >
            <MoreHorizontal aria-hidden="true" />
          </IconButton>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-60" data-testid="toolbar-more-menu">
          {more.map((a) => {
            const Icon = a.icon
            const keys = actionShortcut(a)
            return (
              <DropdownMenuItem
                key={a.id}
                variant={a.destructive ? 'destructive' : 'default'}
                onSelect={() => ctx && a.run(ctx)}
                data-testid={`toolbar-more-${a.id}`}
              >
                <Icon aria-hidden="true" />
                {actionLabel(lt, a)}
                {keys ? <DropdownMenuShortcut>{keys}</DropdownMenuShortcut> : null}
              </DropdownMenuItem>
            )
          })}
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  )
}
