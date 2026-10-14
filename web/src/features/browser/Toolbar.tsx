import {
  ChevronDown,
  FilePlus2,
  FolderPlus,
  FolderUp,
  Globe,
  Link as LinkIcon,
  PanelRight,
  Plus,
  Upload,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { cn } from '@/lib/utils'

export type ToolbarNewKind = 'folder' | 'file' | 'template' | 'symlink'

export interface ToolbarProps {
  onNew: (kind: ToolbarNewKind, opts?: { template?: string }) => void
  /** "Upload ▾" (SPEC §8.8): files or a folder into the open directory. */
  onUpload?: (kind: 'file' | 'folder') => void
  /** "Remote" (SPEC §8.10): remote download dialog; undefined hides the button (feature off). */
  onRemote?: (() => void) | undefined
  /** Template names for "Baru → Dari template" (GET /files/templates). */
  templates?: string[]
  /** Contextual file-operation buttons (`ToolbarActions`). */
  actions?: ReactNode
  compact?: boolean
  previewOpen?: boolean
  onTogglePreview?: () => void
  columnsMenu?: ReactNode
  sortMenu?: ReactNode
  viewSwitcher?: ReactNode
}

/** Toolbar (SPEC §9.3): "Baru", file operations (9b), upload (12a), remote
 *  download (13), preview, sort and columns. */
export function Toolbar({
  onNew,
  onUpload,
  onRemote,
  templates = [],
  actions,
  compact = false,
  previewOpen = false,
  onTogglePreview,
  columnsMenu,
  sortMenu,
  viewSwitcher,
}: ToolbarProps) {
  const { t } = useTranslation('files')
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
        <DropdownMenuContent align="start" className="w-52">
          <DropdownMenuItem onSelect={() => onNew('folder')} data-testid="toolbar-new-folder">
            <FolderPlus className="text-icon-folder" aria-hidden="true" />
            {t('toolbar.newFolder')}
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onNew('file')} data-testid="toolbar-new-file">
            <FilePlus2 aria-hidden="true" />
            {t('toolbar.newFile')}
          </DropdownMenuItem>
          <DropdownMenuSub>
            <DropdownMenuSubTrigger data-testid="toolbar-new-template">
              <FilePlus2 aria-hidden="true" />
              {t('toolbar.newFromTemplate')}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="max-h-72 overflow-auto">
              {templates.length === 0 ? (
                <DropdownMenuItem onSelect={() => onNew('template')}>
                  {t('toolbar.newFromTemplate')}
                </DropdownMenuItem>
              ) : (
                templates.map((name) => (
                  <DropdownMenuItem
                    key={name}
                    onSelect={() => onNew('template', { template: name })}
                    data-testid={`toolbar-template-${name}`}
                  >
                    {`.${name}`}
                  </DropdownMenuItem>
                ))
              )}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => onNew('symlink')} data-testid="toolbar-new-symlink">
            <LinkIcon aria-hidden="true" />
            {t('toolbar.newSymlink')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-1"
            data-testid="toolbar-upload"
            aria-label={compact ? t('toolbar.upload') : undefined}
          >
            <Upload aria-hidden="true" />
            {!compact ? <span>{t('toolbar.upload')}</span> : null}
            <ChevronDown className="size-3 opacity-70" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          <DropdownMenuItem onSelect={() => onUpload?.('file')} data-testid="toolbar-upload-file">
            <Upload aria-hidden="true" />
            {t('toolbar.uploadFile')}
          </DropdownMenuItem>
          <DropdownMenuItem
            onSelect={() => onUpload?.('folder')}
            data-testid="toolbar-upload-folder"
          >
            <FolderUp aria-hidden="true" />
            {t('toolbar.uploadFolder')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {!compact ? (
        <>
          {onRemote ? (
            <IconButton label={t('toolbar.remote')} onClick={onRemote} data-testid="toolbar-remote">
              <Globe aria-hidden="true" />
            </IconButton>
          ) : null}
          <div className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
          {actions}
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
          {actions}
        </div>
      )}
    </div>
  )
}
