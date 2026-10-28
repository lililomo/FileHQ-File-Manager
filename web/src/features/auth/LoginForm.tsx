import { zodResolver } from '@hookform/resolvers/zod'
import { AlertCircle, Eye, EyeOff, ShieldCheck } from 'lucide-react'
import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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

/** `details.fields.totp` of a VALIDATION_FAILED answer (Step 23a). */
function totpReason(e: unknown): string | undefined {
  return e instanceof ApiError && e.code === 'VALIDATION_FAILED' ? e.fields.totp : undefined
}

export function LoginForm({ onSuccess }: { onSuccess: (res: LoginResponse) => void }) {
  const { t } = useTranslation('auth')
  const {
    register,
    handleSubmit,
    setFocus,
    getValues,
    formState: { errors, isSubmitting },
  } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  })
  const [serverError, setServerError] = useState<ServerError | null>(null)
  const [showPassword, setShowPassword] = useState(false)
  const [capsLock, setCapsLock] = useState(false)
  // Second step (2FA): shown after the server answered totp_required.
  const [secondStep, setSecondStep] = useState(false)
  const [useRecovery, setUseRecovery] = useState(false)
  const [code, setCode] = useState('')
  const [codeError, setCodeError] = useState<string | null>(null)
  const [verifying, setVerifying] = useState(false)
  const codeRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    setFocus('username')
  }, [setFocus])

  useEffect(() => {
    if (secondStep) codeRef.current?.focus()
  }, [secondStep, useRecovery])

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

  const finish = (res: LoginResponse) => {
    if (res.recoveryCodesRemaining === 0) toast.warning(t('login.totp.noCodesLeft'))
    else if (typeof res.recoveryCodesRemaining === 'number')
      toast.info(t('login.totp.codesLeft', { count: res.recoveryCodesRemaining }))
    onSuccess(res)
  }

  const onSubmit = handleSubmit(async (values) => {
    setServerError(null)
    try {
      finish(await login(values.username, values.password))
    } catch (e) {
      if (totpReason(e) === 'totp_required') {
        setCode('')
        setCodeError(null)
        setUseRecovery(false)
        setSecondStep(true)
        return
      }
      setServerError(mapError(e))
    }
  })

  const onSubmitCode = async (e: FormEvent) => {
    e.preventDefault()
    const value = code.trim()
    if (!value) {
      setCodeError(t('login.totp.required'))
      codeRef.current?.focus()
      return
    }
    setCodeError(null)
    setServerError(null)
    setVerifying(true)
    try {
      const { username, password } = getValues()
      finish(
        await login(username, password, useRecovery ? { recoveryCode: value } : { totp: value }),
      )
    } catch (err) {
      const reason = totpReason(err)
      if (reason === 'totp_invalid') {
        setCodeError(t(useRecovery ? 'login.totp.invalidRecovery' : 'login.totp.invalid'))
        setCode('')
        codeRef.current?.focus()
      } else if (err instanceof ApiError && err.code === 'RATE_LIMITED') {
        setServerError(mapError(err))
      } else {
        // password changed meanwhile, account disabled, network …: start over
        setSecondStep(false)
        setServerError(mapError(err))
      }
    } finally {
      setVerifying(false)
    }
  }

  const onKey = (e: KeyboardEvent<HTMLInputElement>) =>
    setCapsLock(typeof e.getModifierState === 'function' && e.getModifierState('CapsLock'))

  let message: string | null = null
  if (serverError) {
    switch (serverError.kind) {
      case 'locked':
        message = t('login.errorLocked', { seconds: serverError.seconds })
        break
      case 'rateLimited':
        message = t(secondStep ? 'login.totp.rateLimited' : 'login.errorRateLimited', {
          seconds: serverError.seconds,
        })
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

  const alert = message ? (
    <p
      id="login-error"
      role="alert"
      className="flex items-start gap-2 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-sm text-danger"
    >
      <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <span>{message}</span>
    </p>
  ) : null

  if (secondStep) {
    return (
      <form onSubmit={onSubmitCode} noValidate className="space-y-4" data-testid="login-totp-form">
        <div className="flex items-start gap-2 text-sm text-text-muted">
          <ShieldCheck className="mt-0.5 size-4 shrink-0 text-accent" aria-hidden="true" />
          <p id="login-totp-hint">
            {t(useRecovery ? 'login.totp.recoveryHint' : 'login.totp.hint', {
              username: getValues('username').trim(),
            })}
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="login-totp">
            {t(useRecovery ? 'login.totp.recoveryCode' : 'login.totp.code')}
          </Label>
          <Input
            id="login-totp"
            ref={codeRef}
            value={code}
            onChange={(e) => setCode(e.target.value)}
            inputMode={useRecovery ? 'text' : 'numeric'}
            autoComplete="one-time-code"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={useRecovery ? 16 : 6}
            pattern={useRecovery ? undefined : '[0-9]*'}
            placeholder={useRecovery ? 'xxxxx-xxxxx' : '123456'}
            className={useRecovery ? 'font-mono' : 'font-mono text-lg tracking-[0.3em]'}
            aria-describedby={codeError ? 'login-totp-error' : 'login-totp-hint'}
            aria-invalid={codeError ? true : undefined}
            data-testid="login-totp"
          />
          {codeError ? (
            <p id="login-totp-error" role="alert" className="text-xs text-danger">
              {codeError}
            </p>
          ) : null}
        </div>
        {alert}
        <Button
          type="submit"
          className="w-full"
          disabled={verifying || waiting}
          data-testid="login-totp-submit"
        >
          {verifying ? t('login.totp.verifying') : t('login.totp.submit')}
        </Button>
        <div className="flex flex-wrap justify-between gap-2 text-sm">
          <button
            type="button"
            className="text-accent underline-offset-4 hover:underline"
            onClick={() => {
              setUseRecovery((v) => !v)
              setCode('')
              setCodeError(null)
            }}
            data-testid="login-totp-toggle"
          >
            {t(useRecovery ? 'login.totp.useApp' : 'login.totp.useRecovery')}
          </button>
          <button
            type="button"
            className="text-text-muted underline-offset-4 hover:underline"
            onClick={() => {
              setSecondStep(false)
              setServerError(null)
            }}
            data-testid="login-totp-back"
          >
            {t('login.totp.back')}
          </button>
        </div>
      </form>
    )
  }

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
      {alert}
      <Button type="submit" className="w-full" disabled={isSubmitting || waiting}>
        {isSubmitting ? t('login.submitting') : t('login.submit')}
      </Button>
    </form>
  )
}
