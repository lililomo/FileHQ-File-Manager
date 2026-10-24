import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, RotateCcw } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { Controller, useForm, useWatch, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { ApiError } from '@/lib/api'
import { errorMessage } from '@/lib/errors'
import { ensureSession } from '@/features/auth/session'
import { useSessionStore } from '@/stores/session'
import {
  adminKeys,
  deleteLogo,
  listSettings,
  putSettings,
  resetSettings,
  uploadLogo,
  type AdminSetting,
  type SettingValue,
} from './api'
import {
  buildSchema,
  changedKeys,
  GROUP_ORDER,
  toApi,
  toForm,
  type FormValues,
} from './settingsSchema'

function formatValue(v: SettingValue | undefined): string {
  if (v === undefined) return ''
  if (Array.isArray(v)) return v.length ? v.join(', ') : '—'
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  return String(v)
}

/** `/admin/settings` (SPEC §8.17 "Settings", §3.1): grouped form generated
 *  from the server's key list; each field shows its source (default / env /
 *  overridden), the env and default values, and a reset button. Saves apply
 *  immediately (snapshot on the server) — no restart. */
export function SettingsPage() {
  const { t } = useTranslation(['admin', 'common'])
  const qc = useQueryClient()
  const query = useQuery({
    queryKey: adminKeys.settings,
    queryFn: ({ signal }) => listSettings(signal),
    staleTime: 5_000,
  })
  const items = useMemo(() => query.data?.items ?? [], [query.data])
  return (
    <div className="p-4" data-testid="admin-settings">
      <header className="mb-3">
        <h1 className="text-lg font-semibold">{t('admin:settings.title')}</h1>
        <p className="text-sm text-text-muted">{t('admin:settings.intro')}</p>
      </header>
      {query.isPending ? (
        <div className="space-y-2" aria-busy="true">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : query.isError ? (
        <EmptyState icon={AlertTriangle} title={errorMessage(query.error)} />
      ) : (
        <SettingsForm
          key={items.map((i) => `${i.key}=${formatValue(i.value)}:${i.source}`).join('|')}
          items={items}
          onSaved={(next) => {
            qc.setQueryData(adminKeys.settings, { items: next })
            void ensureSession(true)
          }}
        />
      )}
    </div>
  )
}

function SettingsForm({
  items,
  onSaved,
}: {
  items: AdminSetting[]
  onSaved: (items: AdminSetting[]) => void
}) {
  const { t, i18n } = useTranslation(['admin', 'common'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const schema = useMemo(() => buildSchema(items), [items])
  const defaults = useMemo(() => {
    const out: FormValues = {}
    for (const it of items) if (!it.readOnly) out[it.key] = toForm(it)
    return out
  }, [items])
  const {
    control,
    register,
    handleSubmit,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema) as unknown as Resolver<FormValues>,
    defaultValues: defaults,
    mode: 'onChange',
  })
  const values = useWatch({ control }) as FormValues
  const dirty = changedKeys(items, values)
  const [formError, setFormError] = useState<string | null>(null)
  const save = handleSubmit(async (v) => {
    setFormError(null)
    const body: Record<string, SettingValue> = {}
    for (const key of changedKeys(items, v)) {
      const it = items.find((i) => i.key === key)
      if (it) body[key] = toApi(it.type, v[key] as string | boolean)
    }
    if (Object.keys(body).length === 0) return
    try {
      const res = await putSettings(body)
      toast.success(t('admin:settings.saved'))
      onSaved(res.items)
    } catch (e) {
      if (e instanceof ApiError && typeof e.details.key === 'string') {
        const reason = String(e.details.reason ?? 'invalid')
        setError(e.details.key, { message: reason })
        setFormError(t(`admin:settings.errors.${reason}`, { defaultValue: errorMessage(e) }))
        return
      }
      setFormError(errorMessage(e))
    }
  })
  const reset = useMutation({
    mutationFn: (keys: string[]) => resetSettings(keys),
    onSuccess: (res) => {
      toast.success(t('admin:settings.resetDone'))
      onSaved(res.items)
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const overridden = items.filter((i) => i.source === 'override' && !i.readOnly).map((i) => i.key)
  const groups = GROUP_ORDER.map((g) => ({
    group: g,
    items: items.filter((i) => i.group === g),
  })).filter((g) => g.items.length > 0)
  const errorText = (key: string): string | null => {
    const err = errors[key]
    if (!err) return null
    const reason = String(err.message ?? 'invalid')
    return t(`admin:settings.errors.${reason}`, { defaultValue: reason })
  }
  return (
    <form
      onSubmit={(e) => void save(e)}
      className="space-y-6"
      noValidate
      data-testid="settings-form"
    >
      {groups.map(({ group, items: gi }) => (
        <section
          key={group}
          className="rounded-md border border-border"
          data-testid={`settings-group-${group}`}
        >
          <h2 className="border-b border-border bg-surface-2 px-3 py-1.5 text-sm font-semibold">
            {t(`admin:settings.group.${group}`)}
          </h2>
          <div className="divide-y divide-border">
            {gi.map((it) =>
              it.key === 'branding.logo' ? (
                <LogoField key={it.key} item={it} onChanged={onSaved} />
              ) : (
                <div
                  key={it.key}
                  className="grid gap-2 px-3 py-2 sm:grid-cols-[minmax(0,1fr)_18rem_8rem] sm:items-center"
                  data-testid={`setting-${it.key}`}
                >
                  <div className="min-w-0">
                    <Label htmlFor={`setting-${it.key}`} className="text-sm">
                      {t(`admin:settings.key.${it.key}`, { defaultValue: it.key })}
                    </Label>
                    <div className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-text-muted">
                      <code className="font-mono">{it.key}</code>
                      {it.source === 'override' && it.envValue !== undefined ? (
                        <span>
                          {t('admin:settings.envValue', { value: formatValue(it.envValue) })}
                        </span>
                      ) : null}
                      <span>
                        {t('admin:settings.defaultValue', { value: formatValue(it.default) })}
                      </span>
                      {it.type === 'size' ||
                      it.type === 'duration' ||
                      it.type === 'list' ||
                      it.type === 'tz' ? (
                        <span>{t(`admin:settings.hint.${it.type}`)}</span>
                      ) : null}
                      {it.validation?.min !== undefined &&
                      it.validation?.max !== undefined &&
                      it.type === 'int' ? (
                        <span>
                          {t('admin:settings.hint.range', {
                            min: it.validation.min,
                            max: it.validation.max,
                          })}
                        </span>
                      ) : null}
                    </div>
                  </div>
                  <div>
                    {it.type === 'bool' ? (
                      <Controller
                        control={control}
                        name={it.key}
                        render={({ field }) => (
                          <Switch
                            id={`setting-${it.key}`}
                            checked={field.value === true}
                            onCheckedChange={(c) => field.onChange(c)}
                            data-testid={`setting-input-${it.key}`}
                          />
                        )}
                      />
                    ) : it.type === 'enum' ? (
                      <Controller
                        control={control}
                        name={it.key}
                        render={({ field }) => (
                          <Select
                            value={String(field.value)}
                            onValueChange={(v) => field.onChange(v)}
                          >
                            <SelectTrigger
                              id={`setting-${it.key}`}
                              className="h-8"
                              data-testid={`setting-input-${it.key}`}
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {(it.validation?.options ?? []).map((o) => (
                                <SelectItem key={o} value={o}>
                                  {o}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}
                      />
                    ) : (
                      <Input
                        id={`setting-${it.key}`}
                        className="h-8 font-mono text-xs"
                        aria-invalid={!!errors[it.key]}
                        aria-describedby={errors[it.key] ? `setting-${it.key}-error` : undefined}
                        data-testid={`setting-input-${it.key}`}
                        {...register(it.key)}
                      />
                    )}
                    {errorText(it.key) ? (
                      <p
                        className="mt-1 text-xs text-danger"
                        role="alert"
                        id={`setting-${it.key}-error`}
                        data-testid={`setting-error-${it.key}`}
                      >
                        {errorText(it.key)}
                      </p>
                    ) : null}
                  </div>
                  <div className="flex items-center justify-end gap-1">
                    <Badge
                      variant={it.source === 'override' ? 'default' : 'outline'}
                      data-testid={`setting-source-${it.key}`}
                      data-source={it.source}
                    >
                      {t(`admin:settings.source.${it.source}`)}
                    </Badge>
                    {it.source === 'override' ? (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t('admin:settings.reset')}
                        title={t('admin:settings.reset')}
                        disabled={reset.isPending}
                        onClick={() => reset.mutate([it.key])}
                        data-testid={`setting-reset-${it.key}`}
                      >
                        <RotateCcw className="size-4" aria-hidden="true" />
                      </Button>
                    ) : null}
                  </div>
                </div>
              ),
            )}
          </div>
        </section>
      ))}
      {formError ? (
        <p className="text-sm text-danger" role="alert" data-testid="settings-error">
          {formError}
        </p>
      ) : null}
      <div className="sticky bottom-0 flex items-center gap-2 border-t border-border bg-surface py-2">
        <span className="text-sm text-text-muted" data-testid="settings-dirty">
          {dirty.length > 0 ? t('admin:settings.dirty', { count: dirty.length }) : ''}
        </span>
        <span className="flex-1" />
        {overridden.length > 0 ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={reset.isPending}
            onClick={() => reset.mutate(overridden)}
          >
            {t('admin:settings.resetAll')}
          </Button>
        ) : null}
        <Button
          type="submit"
          size="sm"
          disabled={isSubmitting || dirty.length === 0}
          data-testid="settings-save"
        >
          {t('admin:settings.save')}
        </Button>
      </div>
      <span className="sr-only">{lang}</span>
    </form>
  )
}

function LogoField({
  item,
  onChanged,
}: {
  item: AdminSetting
  onChanged: (items: AdminSetting[]) => void
}) {
  const { t } = useTranslation('admin')
  const qc = useQueryClient()
  const input = useRef<HTMLInputElement>(null)
  const logoUrl = useSessionStore((s) => s.config?.logoUrl)
  // the marker row's updatedAt changes on every upload: cache-busts the preview
  const bust = encodeURIComponent(item.updatedAt ?? '')
  const refresh = async () => {
    await ensureSession(true)
    const res = await listSettings()
    onChanged(res.items)
    void qc.invalidateQueries({ queryKey: ['version'] })
  }
  const upload = useMutation({
    mutationFn: (file: File) => uploadLogo(file),
    onSuccess: async () => {
      toast.success(t('settings.logo.uploaded'))
      await refresh()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const remove = useMutation({
    mutationFn: () => deleteLogo(),
    onSuccess: async () => {
      toast.success(t('settings.logo.removed'))
      await refresh()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const has = typeof item.value === 'string' && item.value !== ''
  return (
    <div
      className="grid gap-2 px-3 py-2 sm:grid-cols-[minmax(0,1fr)_18rem_8rem] sm:items-center"
      data-testid="setting-branding.logo"
    >
      <div>
        <div className="text-sm font-medium">{t('settings.logo.title')}</div>
        <div className="text-xs text-text-muted">{t('settings.logo.hint')}</div>
      </div>
      <div className="flex items-center gap-2">
        {has && logoUrl ? (
          <img
            src={`${logoUrl}?v=${bust}`}
            alt=""
            width={32}
            height={32}
            className="size-8 rounded object-contain"
          />
        ) : (
          <span className="text-xs text-text-muted">{t('settings.logo.none')}</span>
        )}
        <input
          ref={input}
          type="file"
          accept="image/png"
          className="sr-only"
          aria-label={t('settings.logo.upload')}
          data-testid="logo-file"
          onChange={(e) => {
            const f = e.target.files?.[0]
            if (f) upload.mutate(f)
            e.target.value = ''
          }}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => input.current?.click()}
          disabled={upload.isPending}
        >
          {t('settings.logo.upload')}
        </Button>
        {has ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => remove.mutate()}
            disabled={remove.isPending}
          >
            {t('settings.logo.remove')}
          </Button>
        ) : null}
      </div>
      <div className="flex justify-end">
        <Badge variant={has ? 'default' : 'outline'}>
          {t(`settings.source.${has ? 'override' : 'default'}`)}
        </Badge>
      </div>
    </div>
  )
}
