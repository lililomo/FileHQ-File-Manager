import { zodResolver } from '@hookform/resolvers/zod'
import { AlertCircle, Eye, EyeOff } from 'lucide-react'
import { useEffect, useState, type KeyboardEvent } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ApiError } from '@/lib/api'
import { errorMessage } from '@/lib/errors'
import { login, type LoginResponse } from './api'

const schema = z.object({
  username: z.string().trim().min(1, 'required').max(64, 'too_long'),
  password: z.string().min(1, 'required').max(1024, 'too_long'),
})
type Values = z.infer<typeof schema>

type ServerError =
  | { kind: 'generic' | 'disabled' | 'network' }
  | { kind: 'locked' | 'rateLimited'; seconds: number }
  | { kind: 'other'; code: string }

function mapError(e: unknown): ServerError {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'ACCOUNT_LOCKED':
        return { kind: 'locked', seconds: e.retryAfter ?? 60 }
      case 'RATE_LIMITED':
        return { kind: 'rateLimited', seconds: e.retryAfter ?? 30 }
      case 'ACCOUNT_DISABLED':
        return { kind: 'disabled' }
      case 'UNAUTHENTICATED':
      case 'VALIDATION_FAILED':
        return { kind: 'generic' }
      case 'NETWORK':
        return { kind: 'network' }
      default:
        return { kind: 'other', code: e.code }
    }
  }
  return { kind: 'other', code: 'UNKNOWN' }
}

export function LoginForm({ onSuccess }: { onSuccess: (res: LoginResponse) => void }) {
  const { t } = useTranslation('auth')
  const {
    register,
    handleSubmit,
    setFocus,
    formState: { errors, isSubmitting },
  } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  })
  const [serverError, setServerError] = useState<ServerError | null>(null)
  const [showPassword, setShowPassword] = useState(false)
  const [capsLock, setCapsLock] = useState(false)

  useEffect(() => {
    setFocus('username')
  }, [setFocus])

  // Count down lockout / rate-limit waits; clear the message when they end.
  useEffect(() => {
    if (!serverError || !('seconds' in serverError)) return
    const id = window.setTimeout(() => {
      setServerError((e) =>
        e && 'seconds' in e ? (e.seconds > 1 ? { ...e, seconds: e.seconds - 1 } : null) : e,
      )
    }, 1000)
    return () => window.clearTimeout(id)
  }, [serverError])

  const onSubmit = handleSubmit(async (values) => {
    setServerError(null)
    try {
      onSuccess(await login(values.username, values.password))
    } catch (e) {
      setServerError(mapError(e))
    }
  })

  const onKey = (e: KeyboardEvent<HTMLInputElement>) =>
    setCapsLock(typeof e.getModifierState === 'function' && e.getModifierState('CapsLock'))

  let message: string | null = null
  if (serverError) {
    switch (serverError.kind) {
      case 'locked':
        message = t('login.errorLocked', { seconds: serverError.seconds })
        break
      case 'rateLimited':
        message = t('login.errorRateLimited', { seconds: serverError.seconds })
        break
      case 'disabled':
        message = t('login.errorDisabled')
        break
      case 'network':
        message = t('login.errorNetwork')
        break
      case 'generic':
        message = t('login.errorGeneric')
        break
      default:
        message = errorMessage(serverError.code)
    }
  }
  const waiting = serverError !== null && 'seconds' in serverError && serverError.seconds > 0

  return (
    <form onSubmit={onSubmit} noValidate className="space-y-4" data-testid="login-form">
      <div className="space-y-1.5">
        <Label htmlFor="login-username">{t('login.username')}</Label>
        <Input
          id="login-username"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          aria-invalid={errors.username ? true : undefined}
          aria-describedby={errors.username ? 'login-username-error' : undefined}
          {...register('username')}
        />
        {errors.username ? (
          <p id="login-username-error" className="text-xs text-danger">
            {errors.username.message === 'too_long'
              ? t('changePassword.errors.too_long')
              : t('login.required.username')}
          </p>
        ) : null}
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="login-password">{t('login.password')}</Label>
        <div className="relative">
          <Input
            id="login-password"
            type={showPassword ? 'text' : 'password'}
            autoComplete="current-password"
            className="pr-9"
            aria-invalid={errors.password ? true : undefined}
            aria-describedby={errors.password ? 'login-password-error' : undefined}
            onKeyUp={onKey}
            onKeyDown={onKey}
            {...register('password')}
          />
          <IconButton
            type="button"
            size="icon-sm"
            label={showPassword ? t('login.hidePassword') : t('login.showPassword')}
            aria-pressed={showPassword}
            className="absolute top-0.5 right-0.5 size-7"
            onClick={() => setShowPassword((v) => !v)}
          >
            {showPassword ? <EyeOff aria-hidden="true" /> : <Eye aria-hidden="true" />}
          </IconButton>
        </div>
        {errors.password ? (
          <p id="login-password-error" className="text-xs text-danger">
            {t('login.required.password')}
          </p>
        ) : null}
        {capsLock ? (
          <p className="text-xs text-warning" role="status">
            {t('login.capsLock')}
          </p>
        ) : null}
      </div>
      {message ? (
        <p
          id="login-error"
          role="alert"
          className="flex items-start gap-2 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-sm text-danger"
        >
          <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <span>{message}</span>
        </p>
      ) : null}
      <Button type="submit" className="w-full" disabled={isSubmitting || waiting}>
        {isSubmitting ? t('login.submitting') : t('login.submit')}
      </Button>
    </form>
  )
}
