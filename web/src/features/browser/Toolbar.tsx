import {
  ChevronDown,
  Copy,
  FilePlus2,
  FolderPlus,
  Globe,
  MoreHorizontal,
  PanelRight,
  Pencil,
  Plus,
  Scissors,
  ClipboardPaste,
  Trash2,
  Upload,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

export interface ToolbarProps {
  onNewFolder: () => void
  onNewFile: () => void
  compact?: boolean
  previewOpen?: boolean
  onTogglePreview?: () => void
  columnsMenu?: ReactNode
  sortMenu?: ReactNode
  viewSwitcher?: ReactNode
}

/** Toolbar (SPEC §9.3): "Baru", preview, sort and columns work; file operations
 *  (9b), upload (12a), remote download (13) and grid views (8) are placeholders. */
export function Toolbar({
  onNewFolder,
  onNewFile,
  compact = false,
  previewOpen = false,
  onTogglePreview,
  columnsMenu,
  sortMenu,
  viewSwitcher,
}: ToolbarProps) {
  const { t } = useTranslation('files')
  const soon = t('toolbar.comingSoon')
  const disabledIcon = (label: string, Icon: typeof Copy) => (
    <Tooltip key={label}>
      <TooltipTrigger asChild>
        <span tabIndex={-1} className="inline-flex">
          <Button
            variant="ghost"
            size="icon"
            disabled
            aria-label={label}
            className="text-text-muted"
          >
            <Icon aria-hidden="true" />
          </Button>
        </span>
      </TooltipTrigger>
      <TooltipContent>
        {label} · {soon}
      </TooltipContent>
    </Tooltip>
  )
  return (
    <div
      className="flex h-10 shrink-0 items-center gap-1 border-b border-border bg-surface px-2"
      role="toolbar"
      aria-label={t('toolbar.label')}
      data-testid="toolbar"
    >
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="primary" size="sm" className="h-8 gap-1" data-testid="toolbar-new">
            <Plus aria-hidden="true" />
            {!compact ? <span>{t('toolbar.new')}</span> : null}
            <ChevronDown className="size-3.5 opacity-80" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-48">
          <DropdownMenuItem onSelect={onNewFolder} data-testid="toolbar-new-folder">
            <FolderPlus className="text-icon-folder" aria-hidden="true" />
            {t('toolbar.newFolder')}
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={onNewFile} data-testid="toolbar-new-file">
            <FilePlus2 aria-hidden="true" />
            {t('toolbar.newFile')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <span tabIndex={-1} className="inline-flex">
            <Button variant="outline" size="sm" className="h-8 gap-1" disabled>
              <Upload aria-hidden="true" />
              {!compact ? <span>{t('toolbar.upload')}</span> : null}
            </Button>
          </span>
        </TooltipTrigger>
        <TooltipContent>
          {t('toolbar.upload')} · {soon}
        </TooltipContent>
      </Tooltip>
      {!compact ? (
        <>
          {disabledIcon(t('toolbar.remote'), Globe)}
          <div className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
          {disabledIcon(t('toolbar.copy'), Copy)}
          {disabledIcon(t('toolbar.cut'), Scissors)}
          {disabledIcon(t('toolbar.paste'), ClipboardPaste)}
          {disabledIcon(t('toolbar.rename'), Pencil)}
          {disabledIcon(t('toolbar.delete'), Trash2)}
          <div className="ml-auto flex items-center gap-1">
            {viewSwitcher}
            {sortMenu}
            {columnsMenu}
            {onTogglePreview ? (
              <IconButton
                label={t('toolbar.preview')}
                shortcut="Alt+P"
                onClick={onTogglePreview}
                aria-pressed={previewOpen}
                className={cn(previewOpen && 'bg-surface-2 text-text')}
                data-testid="toolbar-preview"
              >
                <PanelRight aria-hidden="true" />
              </IconButton>
            ) : null}
          </div>
        </>
      ) : (
        <div className="ml-auto flex items-center gap-1">
          {viewSwitcher}
          {sortMenu}
          <IconButton label={t('toolbar.more')} disabled>
            <MoreHorizontal aria-hidden="true" />
          </IconButton>
        </div>
      )}
    </div>
  )
}
