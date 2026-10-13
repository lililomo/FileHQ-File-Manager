import { Columns3 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  ContextMenuCheckboxItem,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
} from '@/components/ui/context-menu'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  ALL_COLUMNS,
  CONTEXT_COLUMNS,
  DEFAULT_COLUMNS_PREF,
  type ColumnId,
  type ColumnsPref,
} from './columns'

export interface ColumnMenuProps {
  columns: ColumnsPref
  onChange: (next: ColumnsPref) => void
  allowContextColumns?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
}

function toggleVisible(pref: ColumnsPref, id: ColumnId): ColumnsPref {
  const visible = pref.visible.includes(id)
    ? pref.visible.filter((c) => c !== id)
    : [...pref.visible, id]
  return { ...pref, visible }
}

/** Column picker as a toolbar dropdown (SPEC §8.4). */
export function ColumnMenu({
  columns,
  onChange,
  allowContextColumns = false,
  open,
  onOpenChange,
}: ColumnMenuProps) {
  const { t } = useTranslation('files')
  const ids = columns.order.filter((id) => allowContextColumns || !CONTEXT_COLUMNS.includes(id))
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={t('toolbar.columns')}
              className="text-text-muted hover:text-text"
              data-testid="toolbar-columns"
            >
              <Columns3 aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{t('toolbar.columns')}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel>{t('columns.menuTitle')}</DropdownMenuLabel>
        {ids.map((id) => (
          <DropdownMenuCheckboxItem
            key={id}
            checked={columns.visible.includes(id)}
            disabled={id === 'name'}
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={() => onChange(toggleVisible(columns, id))}
            data-testid={`column-toggle-${id}`}
          >
            {t(`columns.${id}` as 'columns.name')}
          </DropdownMenuCheckboxItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => onChange(DEFAULT_COLUMNS_PREF)}
          data-testid="columns-reset"
        >
          {t('columns.reset')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** Same picker as context-menu content for the grid header. */
export function ColumnContextMenuItems({
  columns,
  onChange,
  allowContextColumns = false,
}: Omit<ColumnMenuProps, 'open' | 'onOpenChange'>) {
  const { t } = useTranslation('files')
  const ids = ALL_COLUMNS.filter((id) => allowContextColumns || !CONTEXT_COLUMNS.includes(id))
  return (
    <>
      <ContextMenuLabel>{t('columns.menuTitle')}</ContextMenuLabel>
      {ids.map((id) => (
        <ContextMenuCheckboxItem
          key={id}
          checked={columns.visible.includes(id)}
          disabled={id === 'name'}
          onSelect={(e) => e.preventDefault()}
          onCheckedChange={() => onChange(toggleVisible(columns, id))}
        >
          {t(`columns.${id}` as 'columns.name')}
        </ContextMenuCheckboxItem>
      ))}
      <ContextMenuSeparator />
      <ContextMenuItem onSelect={() => onChange(DEFAULT_COLUMNS_PREF)}>
        {t('columns.reset')}
      </ContextMenuItem>
    </>
  )
}
