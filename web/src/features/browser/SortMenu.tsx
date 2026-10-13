import { ArrowDownUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import type { SortField } from './api'
import { ALL_COLUMNS, COLUMN_META } from './columns'
import type { SortPref } from './sortCycle'

export interface SortMenuProps {
  sort: SortPref
  onSortChange: (next: SortPref) => void
  foldersFirst: boolean
  onFoldersFirstChange: (v: boolean) => void
}

const SORT_FIELDS = ALL_COLUMNS.map((id) => COLUMN_META[id].sort).filter((f): f is SortField => !!f)

/** "Urutkan berdasarkan" dropdown (SPEC §8.3; used by all views). */
export function SortMenu({
  sort,
  onSortChange,
  foldersFirst,
  onFoldersFirstChange,
}: SortMenuProps) {
  const { t } = useTranslation('files')
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={t('toolbar.sort')}
              className="text-text-muted hover:text-text"
              data-testid="toolbar-sort"
            >
              <ArrowDownUp aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{t('toolbar.sort')}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel>{t('sort.by')}</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={sort.field}
          onValueChange={(v) => onSortChange({ ...sort, field: v as SortField, dir: sort.dir })}
        >
          {SORT_FIELDS.map((f) => (
            <DropdownMenuRadioItem key={f} value={f} data-testid={`sort-field-${f}`}>
              {t(`sort.field.${f}` as 'sort.field.name')}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup
          value={sort.dir}
          onValueChange={(v) => onSortChange({ ...sort, dir: v === 'desc' ? 'desc' : 'asc' })}
        >
          <DropdownMenuRadioItem value="asc">{t('sort.asc')}</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="desc">{t('sort.desc')}</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          checked={foldersFirst}
          onSelect={(e) => e.preventDefault()}
          onCheckedChange={(v) => onFoldersFirstChange(v === true)}
          data-testid="sort-folders-first"
        >
          {t('sort.foldersFirst')}
        </DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
