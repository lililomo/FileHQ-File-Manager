import { zodResolver } from '@hookform/resolvers/zod'
import { AlertCircle, Check, Circle } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError } from '@/lib/api'
import { errorMessage } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { changePassword } from './api'
import { scorePassword } from './password-strength'

const MAX = 128

function makeSchema(min: number, username: string) {
  const uname = username.toLowerCase()
  return z
    .object({
      currentPassword: z.string().min(1, 'required'),
      newPassword: z
        .string()
        .min(1, 'required')
        .min(min, 'too_short')
        .max(MAX, 'too_long')
        .refine((v) => !uname || !v.toLowerCase().includes(uname), 'contains_username'),
      confirm: z.string(),
    })
    .refine((v) => v.newPassword !== v.currentPassword, {
      path: ['newPassword'],
      message: 'same_as_current',
    })
    .refine((v) => v.confirm === v.newPassword, { path: ['confirm'], message: 'mismatch' })
}
type Values = { currentPassword: string; newPassword: string; confirm: string }

export interface ChangePasswordFormProps {
  minLength: number
  username: string
  onSuccess: () => void | Promise<void>
}

export function ChangePasswordForm({ minLength, username, onSuccess }: ChangePasswordFormProps) {
  const { t } = useTranslation('auth')
  const schema = useMemo(() => makeSchema(minLength, username), [minLength, username])
  const {
    register,
    handleSubmit,
    control,
    setError,
    setFocus,
    formState: { errors, isSubmitting },
  } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { currentPassword: '', newPassword: '', confirm: '' },
    mode: 'onChange',
  })
  const [formError, setFormError] = useState<string | null>(null)
  useEffect(() => {
    setFocus('currentPassword')
  }, [setFocus])

  const current = useWatch({ control, name: 'currentPassword' })
  const next = useWatch({ control, name: 'newPassword' })
  const confirm = useWatch({ control, name: 'confirm' })
  const rules = [
    {
      key: 'minLength',
      ok: next.length >= minLength,
      label: t('changePassword.rules.minLength', { min: minLength }),
    },
    {
      key: 'maxLength',
      ok: next.length > 0 && next.length <= MAX,
      label: t('changePassword.rules.maxLength'),
    },
    {
      key: 'notSame',
      ok: next.length > 0 && next !== current,
      label: t('changePassword.rules.notSameAsCurrent'),
    },
    {
      key: 'noUsername',
      ok: next.length > 0 && (!username || !next.toLowerCase().includes(username.toLowerCase())),
      label: t('changePassword.rules.noUsername'),
    },
    {
      key: 'match',
      ok: next.length > 0 && confirm === next,
      label: t('changePassword.rules.match'),
    },
  ]
  const score = scorePassword(next, username)
  const strengthLabel = (['weak', 'weak', 'fair', 'good', 'strong'] as const)[score]
  const strengthColor = ['bg-danger', 'bg-danger', 'bg-warning', 'bg-success', 'bg-success'][score]

  const reasonText = (reason: string | undefined) =>
    reason
      ? t(`changePassword.errors.${reason}` as 'changePassword.errors.required', { min: minLength })
      : null

  const onSubmit = handleSubmit(async (values) => {
    setFormError(null)
    try {
      await changePassword(values.currentPassword, values.newPassword)
      await onSuccess()
    } catch (e) {
      if (e instanceof ApiError && e.code === 'VALIDATION_FAILED') {
        const fields = e.fields
        if (fields.newPassword) setError('newPassword', { message: fields.newPassword })
        if (fields.currentPassword) setError('currentPassword', { message: fields.currentPassword })
        if (!fields.newPassword && !fields.currentPassword) setFormError(errorMessage(e))
      } else if (e instanceof ApiError && e.code === 'RATE_LIMITED') {
        setFormError(t('login.errorRateLimited', { seconds: e.retryAfter ?? 60 }))
      } else {
        setFormError(errorMessage(e))
      }
    }
  })

  return (
    <form onSubmit={onSubmit} noValidate className="space-y-4" data-testid="change-password-form">
      <div className="space-y-1.5">
        <Label htmlFor="cp-current">{t('changePassword.current')}</Label>
        <Input
          id="cp-current"
          type="password"
          autoComplete="current-password"
          aria-invalid={errors.currentPassword ? true : undefined}
          aria-describedby={errors.currentPassword ? 'cp-current-error' : undefined}
          {...register('currentPassword')}
        />
        {errors.currentPassword ? (
          <p id="cp-current-error" className="text-xs text-danger">
            {reasonText(errors.currentPassword.message)}
          </p>
        ) : null}
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="cp-new">{t('changePassword.new')}</Label>
        <Input
          id="cp-new"
          type="password"
          autoComplete="new-password"
          aria-invalid={errors.newPassword ? true : undefined}
          aria-describedby={cn('cp-rules', errors.newPassword && 'cp-new-error')}
          {...register('newPassword')}
        />
        {errors.newPassword ? (
          <p id="cp-new-error" className="text-xs text-danger">
            {reasonText(errors.newPassword.message)}
          </p>
        ) : null}
        <div className="space-y-1" aria-live="polite">
          <div className="flex items-center justify-between text-xs text-text-muted">
            <span>{t('changePassword.strength.label')}</span>
            <span>{next ? t(`changePassword.strength.${strengthLabel}`) : '—'}</span>
          </div>
          <div className="flex gap-1" aria-hidden="true">
            {[1, 2, 3, 4].map((n) => (
              <span
                key={n}
                className={cn(
                  'h-1 flex-1 rounded-full bg-surface-2',
                  next && n <= Math.max(1, score) && strengthColor,
                )}
              />
            ))}
          </div>
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="cp-confirm">{t('changePassword.confirm')}</Label>
        <Input
          id="cp-confirm"
          type="password"
          autoComplete="new-password"
          aria-invalid={errors.confirm ? true : undefined}
          aria-describedby={errors.confirm ? 'cp-confirm-error' : undefined}
          {...register('confirm')}
        />
        {errors.confirm ? (
          <p id="cp-confirm-error" className="text-xs text-danger">
            {reasonText(errors.confirm.message)}
          </p>
        ) : null}
      </div>
      <ul id="cp-rules" className="space-y-1 text-xs">
        {rules.map((r) => (
          <li
            key={r.key}
            className={cn('flex items-center gap-2', r.ok ? 'text-success' : 'text-text-muted')}
          >
            {r.ok ? (
              <Check className="size-3.5" aria-hidden="true" />
            ) : (
              <Circle className="size-3.5" aria-hidden="true" />
            )}
            <span>{r.label}</span>
          </li>
        ))}
      </ul>
      {formError ? (
        <p
          role="alert"
          className="flex items-start gap-2 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-sm text-danger"
        >
          <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <span>{formError}</span>
        </p>
      ) : null}
      <Button type="submit" className="w-full" disabled={isSubmitting}>
        {isSubmitting ? t('changePassword.submitting') : t('changePassword.submit')}
      </Button>
    </form>
  )
}
