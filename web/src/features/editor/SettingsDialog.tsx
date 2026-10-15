import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  EDITOR_FONTS,
  EDITOR_THEMES,
  setEditorPref,
  useEditorPrefs,
  type EditorPrefs,
  type EditorTheme,
} from './prefs'

/** Editor preferences (`editor.*`, saved on the server through the prefs store). */
export function SettingsDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const { t } = useTranslation('editor')
  const p = useEditorPrefs()
  const themeLabel: Record<EditorTheme, string> = {
    system: t('settings.themeSystem'),
    light: t('settings.themeLight'),
    dark: t('settings.themeDark'),
    'high-contrast': t('settings.themeHc'),
  }
  const row = (label: string, control: React.ReactNode, id: string) => (
    <div className="flex items-center justify-between gap-4 py-1.5">
      <Label htmlFor={id}>{label}</Label>
      {control}
    </div>
  )
  const toggle = (key: keyof EditorPrefs, label: string) =>
    row(
      label,
      <Switch
        id={`ed-${key}`}
        checked={p[key] as boolean}
        onCheckedChange={(v) => setEditorPref(key, v as never)}
      />,
      `ed-${key}`,
    )
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md" data-testid="editor-settings">
        <DialogHeader>
          <DialogTitle>{t('settings.title')}</DialogTitle>
        </DialogHeader>
        <div className="divide-y divide-border text-sm">
          {row(
            t('settings.theme'),
            <Select value={p.theme} onValueChange={(v) => setEditorPref('theme', v as EditorTheme)}>
              <SelectTrigger id="ed-theme" className="w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {EDITOR_THEMES.map((th) => (
                  <SelectItem key={th} value={th}>
                    {themeLabel[th]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>,
            'ed-theme',
          )}
          {row(
            t('settings.fontFamily'),
            <Select value={p.font_family} onValueChange={(v) => setEditorPref('font_family', v)}>
              <SelectTrigger id="ed-font" className="w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {EDITOR_FONTS.map((f) => (
                  <SelectItem key={f} value={f}>
                    {f}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>,
            'ed-font',
          )}
          {row(
            t('settings.fontSize'),
            <Input
              id="ed-size"
              type="number"
              min={10}
              max={24}
              className="w-24"
              value={p.font_size}
              onChange={(e) =>
                setEditorPref('font_size', Math.min(24, Math.max(10, Number(e.target.value) || 13)))
              }
            />,
            'ed-size',
          )}
          {row(
            t('settings.tabSize'),
            <Select
              value={String(p.tab_size)}
              onValueChange={(v) => setEditorPref('tab_size', Number(v) as 2 | 4 | 8)}
            >
              <SelectTrigger id="ed-tab" className="w-24">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {[2, 4, 8].map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>,
            'ed-tab',
          )}
          {toggle('insert_spaces', t('settings.insertSpaces'))}
          {toggle('word_wrap', t('settings.wordWrap'))}
          {toggle('minimap', t('settings.minimap'))}
          {row(
            t('settings.renderWhitespace'),
            <Select
              value={p.render_whitespace}
              onValueChange={(v) =>
                setEditorPref('render_whitespace', v as EditorPrefs['render_whitespace'])
              }
            >
              <SelectTrigger id="ed-ws" className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">{t('settings.wsNone')}</SelectItem>
                <SelectItem value="boundary">{t('settings.wsBoundary')}</SelectItem>
                <SelectItem value="all">{t('settings.wsAll')}</SelectItem>
              </SelectContent>
            </Select>,
            'ed-ws',
          )}
          {toggle('line_numbers', t('settings.lineNumbers'))}
          {toggle('bracket_pair_colorization', t('settings.bracketColors'))}
          {toggle('auto_save', t('settings.autoSave'))}
          {row(
            t('settings.autoSaveInterval'),
            <Input
              id="ed-autosave"
              type="number"
              min={2}
              max={600}
              className="w-24"
              disabled={!p.auto_save}
              value={p.auto_save_interval}
              onChange={(e) =>
                setEditorPref(
                  'auto_save_interval',
                  Math.min(600, Math.max(2, Number(e.target.value) || 30)),
                )
              }
            />,
            'ed-autosave',
          )}
        </div>
        <DialogFooter>
          <Button onClick={() => onOpenChange(false)}>{t('settings.done')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
