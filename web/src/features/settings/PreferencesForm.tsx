import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { VIEW_MODES } from '@/features/browser/api'
import { LANGUAGES, isLanguage, setLanguage } from '@/i18n'
import { setDisplayTimeZone } from '@/lib/format'
import { ACCENTS, DENSITIES, THEME_PREFS, isAccent, isDensity, isThemePref } from '@/lib/theme'
import { usePref, usePrefsStore } from '@/stores/prefs'
import { useUIStore } from '@/stores/ui'

function timeZones(): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (k: string) => string[] }
  try {
    return intl.supportedValuesOf?.('timeZone') ?? []
  } catch {
    return []
  }
}

/** Preferences (SPEC §8.18): stored in `me/preferences` through the prefs
 *  store (local at once, debounced merge patch) and applied immediately —
 *  theme/accent/density on <html>, language via i18next, display zone in
 *  the date formatters. */
export function PreferencesForm() {
  const { t } = useTranslation(['auth', 'common'])
  const setPref = usePrefsStore((s) => s.set)
  const ui = useUIStore()
  const language = usePref<string>('ui.language', '')
  const view = usePref<string>('view.mode', 'details')
  const foldersFirst = usePref<boolean>('view.folders_first', true)
  const confirmDelete = usePref<boolean>('fileops.confirm_delete', true)
  const tz = usePref<string>('ui.timezone', 'auto')
  const zones = useMemo(() => timeZones(), [])
  const select = 'h-8 w-full rounded-md border border-border bg-surface px-2 text-sm'
  const row = 'grid grid-cols-1 items-center gap-1 sm:grid-cols-[14rem_1fr]'

  return (
    <section aria-labelledby="prefs-title" className="space-y-3" data-testid="preferences">
      <h2 id="prefs-title" className="text-base font-semibold">
        {t('auth:profile.prefs.title')}
      </h2>
      <div className={row}>
        <Label htmlFor="pref-theme">{t('auth:profile.prefs.theme')}</Label>
        <select
          id="pref-theme"
          className={select}
          value={ui.theme}
          onChange={(e) => {
            if (!isThemePref(e.target.value)) return
            ui.setTheme(e.target.value)
            setPref('ui.theme', e.target.value)
          }}
          data-testid="pref-theme"
        >
          {THEME_PREFS.map((v) => (
            <option key={v} value={v}>
              {t(`common:theme.${v}`)}
            </option>
          ))}
        </select>
      </div>
      <div className={row}>
        <Label htmlFor="pref-accent">{t('auth:profile.prefs.accent')}</Label>
        <select
          id="pref-accent"
          className={select}
          value={ui.accent}
          onChange={(e) => {
            if (!isAccent(e.target.value)) return
            ui.setAccent(e.target.value)
            setPref('ui.accent', e.target.value)
          }}
        >
          {ACCENTS.map((v) => (
            <option key={v} value={v}>
              {t(`auth:profile.prefs.accents.${v}`)}
            </option>
          ))}
        </select>
      </div>
      <div className={row}>
        <Label htmlFor="pref-language">{t('auth:profile.prefs.language')}</Label>
        <select
          id="pref-language"
          className={select}
          value={isLanguage(language) ? language : ''}
          onChange={(e) => {
            const v = e.target.value
            if (isLanguage(v)) void setLanguage(v)
            setPref('ui.language', isLanguage(v) ? v : null)
          }}
          data-testid="pref-language"
        >
          <option value="">{t('auth:profile.prefs.languageDefault')}</option>
          {LANGUAGES.map((v) => (
            <option key={v} value={v}>
              {v === 'id' ? 'Bahasa Indonesia' : 'English'}
            </option>
          ))}
        </select>
      </div>
      <div className={row}>
        <Label htmlFor="pref-view">{t('auth:profile.prefs.view')}</Label>
        <select
          id="pref-view"
          className={select}
          value={view}
          onChange={(e) => setPref('view.mode', e.target.value)}
        >
          {VIEW_MODES.map((v) => (
            <option key={v} value={v}>
              {t(`auth:profile.prefs.views.${v}`)}
            </option>
          ))}
        </select>
      </div>
      <div className={row}>
        <Label htmlFor="pref-density">{t('auth:profile.prefs.density')}</Label>
        <select
          id="pref-density"
          className={select}
          value={ui.density}
          onChange={(e) => {
            if (!isDensity(e.target.value)) return
            ui.setDensity(e.target.value)
            setPref('ui.density', e.target.value)
          }}
        >
          {DENSITIES.map((v) => (
            <option key={v} value={v}>
              {t(`auth:profile.prefs.densities.${v}`)}
            </option>
          ))}
        </select>
      </div>
      <div className={row}>
        <Label htmlFor="pref-tz">{t('auth:profile.prefs.timezone')}</Label>
        <select
          id="pref-tz"
          className={select}
          value={tz}
          onChange={(e) => {
            setDisplayTimeZone(e.target.value)
            setPref('ui.timezone', e.target.value)
          }}
          data-testid="pref-timezone"
        >
          <option value="auto">
            {t('auth:profile.prefs.timezoneAuto', {
              tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
            })}
          </option>
          {zones.map((z) => (
            <option key={z} value={z}>
              {z}
            </option>
          ))}
        </select>
      </div>
      <div className={row}>
        <Label htmlFor="pref-folders-first">{t('auth:profile.prefs.foldersFirst')}</Label>
        <Switch
          id="pref-folders-first"
          checked={foldersFirst}
          onCheckedChange={(v) => setPref('view.folders_first', v)}
        />
      </div>
      <div className={row}>
        <Label htmlFor="pref-confirm-delete">{t('auth:profile.prefs.confirmDelete')}</Label>
        <Switch
          id="pref-confirm-delete"
          checked={confirmDelete}
          onCheckedChange={(v) => setPref('fileops.confirm_delete', v)}
          data-testid="pref-confirm-delete"
        />
      </div>
    </section>
  )
}
