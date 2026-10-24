import { useTranslation } from 'react-i18next'
import {
  AArrowDown,
  AArrowUp,
  Eye,
  FileSearch,
  History,
  ListOrdered,
  Maximize2,
  PanelLeft,
  Replace,
  Save,
  SaveAll,
  Search,
  Settings2,
  SunMoon,
  WrapText,
  X,
} from 'lucide-react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { Separator } from '@/components/ui/separator'
import { formatKeys } from '@/lib/shortcuts'
import { useEditorStore, type EditorTab } from '@/stores/editor'
import { useWorkspace } from './controller'
import { EDITOR_THEMES, setEditorPref, useEditorPrefs, type EditorTheme } from './prefs'

export interface ToolbarProps {
  tab: EditorTab | null
  mode: 'page' | 'modal'
  explorerOpen: boolean
  onToggleExplorer: () => void
  onSettings: () => void
  onMaximize?: (() => void) | undefined
  onClose?: (() => void) | undefined
}

/** Editor toolbar (SPEC §9.3 wireframe): Save, Save all, Find, Replace, Go to
 *  line, Format, font size ±, theme, settings, explorer toggle. */
export function Toolbar(p: ToolbarProps) {
  const { t } = useTranslation('editor')
  const ws = useWorkspace()
  const prefs = useEditorPrefs()
  const dirtyCount = useEditorStore((s) => s.tabs.filter((x) => x.dirty).length)
  const ready = !!p.tab && p.tab.status === 'ready'
  const keys = (id: string) => formatKeys(id).join('')
  const themeLabel: Record<EditorTheme, string> = {
    system: t('settings.themeSystem'),
    light: t('settings.themeLight'),
    dark: t('settings.themeDark'),
    'high-contrast': t('settings.themeHc'),
  }
  return (
    <div
      role="toolbar"
      aria-label={t('title')}
      data-testid="editor-toolbar"
      className="flex h-10 min-w-0 shrink-0 items-center gap-0.5 overflow-x-auto border-b border-border bg-surface px-2"
    >
      <IconButton
        label={t('toolbar.explorer')}
        onClick={p.onToggleExplorer}
        aria-pressed={p.explorerOpen}
        data-testid="toggle-explorer"
      >
        <PanelLeft />
      </IconButton>
      <Separator orientation="vertical" className="mx-1 h-5" />
      <IconButton
        label={t('toolbar.save')}
        shortcut={keys('Ctrl+S')}
        disabled={!ready || !p.tab?.dirty || p.tab.readOnly}
        onClick={() => p.tab && void ws.save(p.tab.path)}
        data-testid="btn-save"
      >
        <Save />
      </IconButton>
      <IconButton
        label={t('toolbar.saveAll')}
        shortcut={keys('Ctrl+Shift+S')}
        disabled={dirtyCount === 0}
        onClick={() => void ws.saveAll()}
        data-testid="btn-save-all"
      >
        <SaveAll />
      </IconButton>
      <Separator orientation="vertical" className="mx-1 h-5" />
      <IconButton
        label={t('toolbar.find')}
        shortcut={keys('Ctrl+F')}
        disabled={!ready}
        onClick={() => ws.runEditorAction('actions.find')}
      >
        <Search />
      </IconButton>
      <IconButton
        label={t('toolbar.replace')}
        shortcut={keys('Ctrl+H')}
        disabled={!ready || !!p.tab?.readOnly}
        onClick={() => ws.runEditorAction('editor.action.startFindReplaceAction')}
      >
        <Replace />
      </IconButton>
      <IconButton
        label={t('toolbar.gotoLine')}
        shortcut={keys('Ctrl+G')}
        disabled={!ready}
        onClick={() => ws.runEditorAction('editor.action.gotoLine')}
        data-testid="btn-goto"
      >
        <ListOrdered />
      </IconButton>
      <IconButton
        label={t('toolbar.format')}
        shortcut={keys('Shift+Alt+F')}
        disabled={!ready || !!p.tab?.readOnly}
        onClick={() => p.tab && void ws.formatDocument(p.tab.path)}
      >
        <WrapText />
      </IconButton>
      <Separator orientation="vertical" className="mx-1 h-5" />
      <IconButton
        label={t('toolbar.findInFiles')}
        onClick={() => ws.openFindInFiles()}
        data-testid="btn-find-in-files"
      >
        <FileSearch />
      </IconButton>
      <IconButton
        label={t('toolbar.history')}
        disabled={!ready || !!p.tab?.versionId}
        onClick={() => p.tab && ws.openVersions(p.tab.path)}
        data-testid="btn-history"
      >
        <History />
      </IconButton>
      {p.tab?.language === 'markdown' ? (
        <IconButton
          label={t('toolbar.preview')}
          aria-pressed={!!p.tab.preview}
          onClick={() => p.tab && ws.togglePreview(p.tab.path)}
          data-testid="btn-preview"
          className={p.tab.preview ? 'bg-surface-3' : undefined}
        >
          <Eye />
        </IconButton>
      ) : null}
      <Separator orientation="vertical" className="mx-1 h-5" />
      <IconButton
        label={t('toolbar.fontSmaller')}
        disabled={prefs.font_size <= 10}
        onClick={() => setEditorPref('font_size', Math.max(10, prefs.font_size - 1))}
      >
        <AArrowDown />
      </IconButton>
      <span className="w-6 text-center text-xs tabular-nums text-text-muted" aria-hidden>
        {prefs.font_size}
      </span>
      <IconButton
        label={t('toolbar.fontLarger')}
        disabled={prefs.font_size >= 24}
        onClick={() => setEditorPref('font_size', Math.min(24, prefs.font_size + 1))}
      >
        <AArrowUp />
      </IconButton>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <IconButton label={t('toolbar.theme')} data-testid="btn-theme">
            <SunMoon />
          </IconButton>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          <DropdownMenuRadioGroup
            value={prefs.theme}
            onValueChange={(v) => setEditorPref('theme', v as EditorTheme)}
          >
            {EDITOR_THEMES.map((th) => (
              <DropdownMenuRadioItem key={th} value={th}>
                {themeLabel[th]}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      <IconButton label={t('toolbar.settings')} onClick={p.onSettings} data-testid="btn-settings">
        <Settings2 />
      </IconButton>
      <div className="ml-auto flex items-center gap-0.5">
        {p.mode === 'modal' && p.onMaximize ? (
          <IconButton
            label={t('toolbar.maximize')}
            onClick={p.onMaximize}
            data-testid="btn-maximize"
          >
            <Maximize2 />
          </IconButton>
        ) : null}
        {p.onClose ? (
          <IconButton label={t('toolbar.close')} onClick={p.onClose} data-testid="btn-close-editor">
            <X />
          </IconButton>
        ) : null}
      </div>
    </div>
  )
}
