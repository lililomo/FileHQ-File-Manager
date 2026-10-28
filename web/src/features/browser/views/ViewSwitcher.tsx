import { Columns2, Grid2x2, Image, LayoutGrid, List, Rows3, type LucideIcon } from 'lucide-react'
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
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { formatKeys } from '@/lib/shortcuts'
import { VIEW_MODES, type ViewMode } from '../api'

export const VIEW_ICONS: Record<ViewMode, LucideIcon> = {
  details: Rows3,
  list: List,
  tiles: LayoutGrid,
  large: Grid2x2,
  xlarge: Image,
}

export interface ViewSwitcherProps {
  mode: ViewMode
  onModeChange: (mode: ViewMode) => void
  rememberPerFolder: boolean
  onRememberPerFolderChange: (v: boolean) => void
  /** Step 23b: "Dual-pane" toggle (only passed on screens ≥ 1280 px). */
  dualPane?: { enabled: boolean; onChange: (v: boolean) => void } | undefined
}

/** "View" dropdown (SPEC §8.4): Details / List / Tiles / Large icons / Extra
 *  large icons with Ctrl+1…5, plus "remember view per folder". */
export function ViewSwitcher({
  mode,
  onModeChange,
  rememberPerFolder,
  onRememberPerFolderChange,
  dualPane,
}: ViewSwitcherProps) {
  const { t } = useTranslation('files')
  const Icon = VIEW_ICONS[mode]
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={t('toolbar.view')}
              className="text-text-muted hover:text-text"
              data-testid="toolbar-view"
              data-view={mode}
            >
              <Icon aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>
          {t('toolbar.view')} · {t(`view.${mode}`)}
        </TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuLabel>{t('view.label')}</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={mode} onValueChange={(v) => onModeChange(v as ViewMode)}>
          {VIEW_MODES.map((m, i) => {
            const MI = VIEW_ICONS[m]
            return (
              <DropdownMenuRadioItem key={m} value={m} data-testid={`view-mode-${m}`}>
                <MI className="mr-2 size-4 text-text-muted" aria-hidden="true" />
                {t(`view.${m}`)}
                <DropdownMenuShortcut>{formatKeys(`Ctrl+${i + 1}`)}</DropdownMenuShortcut>
              </DropdownMenuRadioItem>
            )
          })}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          checked={rememberPerFolder}
          onSelect={(e) => e.preventDefault()}
          onCheckedChange={(v) => onRememberPerFolderChange(v === true)}
          data-testid="view-remember-folder"
        >
          {t('view.rememberPerFolder')}
        </DropdownMenuCheckboxItem>
        {dualPane ? (
          <DropdownMenuCheckboxItem
            checked={dualPane.enabled}
            onCheckedChange={(v) => dualPane.onChange(v === true)}
            data-testid="view-dual-pane"
          >
            <Columns2 className="mr-2 size-4 text-text-muted" aria-hidden="true" />
            {t('view.dualPane')}
            <DropdownMenuShortcut>{formatKeys('F6')}</DropdownMenuShortcut>
          </DropdownMenuCheckboxItem>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
