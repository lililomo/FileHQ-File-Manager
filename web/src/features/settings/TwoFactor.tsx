import { useMutation } from '@tanstack/react-query'
import { Check, Copy, Download, ShieldCheck, ShieldOff } from 'lucide-react'
import { useEffect, useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { QrCode } from '@/features/share/QrCode'
import { ApiError } from '@/lib/api'
import { errorMessage } from '@/lib/errors'
import { formatDate } from '@/lib/format'
import { useSessionStore } from '@/stores/session'
import { disableTotp, enableTotp, regenerateRecoveryCodes, setupTotp } from './api'

/** 2FA TOTP card of the profile page (SPEC §8.18, Step 23a): status, enrol
 *  (QR + manual secret + first code → recovery codes shown once), disable
 *  (password + code) and recovery-code regeneration (password). */
export function TwoFactor() {
  const { t, i18n } = useTranslation('auth')
  const lang = i18n.resolvedLanguage ?? 'id'
  const user = useSessionStore((s) => s.user)
  const enabled = user?.totpEnabled === true
  const [dialog, setDialog] = useState<'enable' | 'disable' | 'regen' | null>(null)
  const close = () => setDialog(null)
  return (
    <section
      aria-labelledby="twofa-title"
      className="rounded-lg border border-border p-4"
      data-testid="twofa-card"
    >
      <div className="flex flex-wrap items-start gap-3">
        {enabled ? (
          <ShieldCheck className="mt-0.5 size-5 text-success" aria-hidden="true" />
        ) : (
          <ShieldOff className="mt-0.5 size-5 text-text-muted" aria-hidden="true" />
        )}
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 id="twofa-title" className="text-base font-semibold">
              {t('profile.twofa.title')}
            </h2>
            <Badge
              variant={enabled ? 'outline' : 'secondary'}
              className={enabled ? 'text-success' : ''}
              data-testid="twofa-status"
            >
              {t(enabled ? 'profile.twofa.statusOn' : 'profile.twofa.statusOff')}
            </Badge>
          </div>
          <p className="text-sm text-text-muted">
            {enabled && user?.totpEnabledAt
              ? t('profile.twofa.since', {
                  date: formatDate(user.totpEnabledAt, lang, { time: true }),
                })
              : t('profile.twofa.description')}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {enabled ? (
            <>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setDialog('regen')}
                data-testid="twofa-regen"
              >
                {t('profile.twofa.regenerate')}
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="text-danger"
                onClick={() => setDialog('disable')}
                data-testid="twofa-disable"
              >
                {t('profile.twofa.disable')}
              </Button>
            </>
          ) : (
            <Button size="sm" onClick={() => setDialog('enable')} data-testid="twofa-enable">
              {t('profile.twofa.enable')}
            </Button>
          )}
        </div>
      </div>
      {dialog === 'enable' ? <EnableDialog onClose={close} /> : null}
      {dialog === 'disable' ? <DisableDialog onClose={close} /> : null}
      {dialog === 'regen' ? <RegenerateDialog onClose={close} /> : null}
    </section>
  )
}

function apiFieldError(e: unknown, field: string): string | undefined {
  return e instanceof ApiError && e.code === 'VALIDATION_FAILED' ? e.fields[field] : undefined
}

/** Message for a failed TOTP request: field reason, rate limit or generic. */
function useTotpErrorText() {
  const { t } = useTranslation('auth')
  return (e: unknown): string => {
    if (e instanceof ApiError && e.code === 'RATE_LIMITED')
      return t('profile.twofa.errors.rateLimited', { seconds: e.retryAfter ?? 60 })
    return errorMessage(e)
  }
}

// ---------------------------------------------------------------------------
// Recovery codes (shown once)

function RecoveryCodes({
  codes,
  acked,
  onAck,
  username,
}: {
  codes: string[]
  acked: boolean
  onAck: (v: boolean) => void
  username: string
}) {
  const { t } = useTranslation('auth')
  const id = useId()
  const [copied, setCopied] = useState(false)
  const text = codes.join('\n')
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }
  const download = () => {
    const blob = new Blob([`${t('profile.twofa.codes.fileHeader', { username })}\n\n${text}\n`], {
      type: 'text/plain',
    })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `recovery-codes-${username}.txt`
    a.click()
    URL.revokeObjectURL(url)
  }
  return (
    <div className="space-y-3">
      <p className="text-sm text-text-muted">{t('profile.twofa.codes.intro')}</p>
      <ol
        className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-md border border-border bg-surface-2 p-3 font-mono text-sm select-all"
        aria-label={t('profile.twofa.codes.listLabel')}
        data-testid="recovery-codes"
      >
        {codes.map((c) => (
          <li key={c} data-testid="recovery-code">
            {c}
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => void copy()}>
          {copied ? (
            <Check className="size-4" aria-hidden="true" />
          ) : (
            <Copy className="size-4" aria-hidden="true" />
          )}
          {copied ? t('profile.twofa.codes.copied') : t('profile.twofa.codes.copy')}
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={download}>
          <Download className="size-4" aria-hidden="true" />
          {t('profile.twofa.codes.download')}
        </Button>
      </div>
      <div className="flex items-start gap-2">
        <Checkbox
          id={id}
          checked={acked}
          onCheckedChange={(v) => onAck(v === true)}
          className="mt-0.5"
          data-testid="recovery-ack"
        />
        <Label htmlFor={id} className="text-sm leading-snug font-normal">
          {t('profile.twofa.codes.ack')}
        </Label>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Enable: setup → verify → recovery codes

function EnableDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation('auth')
  const errorText = useTotpErrorText()
  const user = useSessionStore((s) => s.user)
  const appName = useSessionStore((s) => s.config?.appName) ?? 'FileManager'
  const [code, setCode] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [codes, setCodes] = useState<string[] | null>(null)
  const [acked, setAcked] = useState(false)
  const [copied, setCopied] = useState(false)
  const setup = useMutation({ mutationFn: setupTotp })
  const { mutate: runSetup } = setup
  useEffect(() => {
    runSetup()
  }, [runSetup])
  const enable = useMutation({
    mutationFn: (c: string) => enableTotp(c),
    onSuccess: (res) => {
      useSessionStore.getState().updateUser({ totpEnabled: true, totpEnabledAt: res.enabledAt })
      setCodes(res.recoveryCodes)
      toast.success(t('profile.twofa.toast.enabled'))
    },
    onError: (e) => {
      if (apiFieldError(e, 'code') === 'totp_invalid') {
        setError(t('profile.twofa.errors.invalidCode'))
        setCode('')
      } else setError(errorText(e))
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!/^\d{6}$/.test(code.trim())) {
      setError(t('profile.twofa.errors.sixDigits'))
      return
    }
    setError(null)
    enable.mutate(code.trim())
  }
  const canClose = codes === null || acked
  const secretGroups = setup.data?.secret.match(/.{1,4}/g)?.join(' ') ?? ''
  const copySecret = async () => {
    if (!setup.data) return
    try {
      await navigator.clipboard.writeText(setup.data.secret)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }
  return (
    <Dialog open onOpenChange={(open) => !open && canClose && onClose()}>
      <DialogContent
        data-testid="twofa-enable-dialog"
        onEscapeKeyDown={(e) => !canClose && e.preventDefault()}
        onInteractOutside={(e) => !canClose && e.preventDefault()}
      >
        {codes ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('profile.twofa.codes.title')}</DialogTitle>
              <DialogDescription>{t('profile.twofa.codes.subtitle')}</DialogDescription>
            </DialogHeader>
            <RecoveryCodes
              codes={codes}
              acked={acked}
              onAck={setAcked}
              username={user?.username ?? ''}
            />
            <DialogFooter>
              <Button type="button" disabled={!acked} onClick={onClose} data-testid="twofa-done">
                {t('profile.twofa.codes.done')}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="space-y-4" noValidate>
            <DialogHeader>
              <DialogTitle>{t('profile.twofa.setup.title')}</DialogTitle>
              <DialogDescription>
                {t('profile.twofa.setup.subtitle', { appName })}
              </DialogDescription>
            </DialogHeader>
            {setup.isError ? (
              <p role="alert" className="text-sm text-danger">
                {errorText(setup.error)}
              </p>
            ) : setup.data ? (
              <div className="flex flex-col items-center gap-3 sm:flex-row sm:items-start">
                <QrCode
                  value={setup.data.otpauthUri}
                  size={176}
                  label={t('profile.twofa.setup.qrLabel')}
                />
                <div className="min-w-0 flex-1 space-y-2 text-sm">
                  <p className="text-text-muted">{t('profile.twofa.setup.manual')}</p>
                  <code
                    className="block rounded-md border border-border bg-surface-2 px-3 py-2 font-mono text-sm break-all select-all"
                    data-testid="totp-secret"
                    data-secret={setup.data.secret}
                  >
                    {secretGroups}
                  </code>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void copySecret()}
                  >
                    {copied ? (
                      <Check className="size-4" aria-hidden="true" />
                    ) : (
                      <Copy className="size-4" aria-hidden="true" />
                    )}
                    {copied ? t('profile.twofa.codes.copied') : t('profile.twofa.setup.copySecret')}
                  </Button>
                </div>
              </div>
            ) : (
              <p className="text-sm text-text-muted">{t('profile.twofa.setup.loading')}</p>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="twofa-code">{t('profile.twofa.setup.code')}</Label>
              <Input
                id="twofa-code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                pattern="[0-9]*"
                autoFocus
                className="font-mono text-lg tracking-[0.3em]"
                aria-invalid={error ? true : undefined}
                aria-describedby={error ? 'twofa-code-error' : undefined}
                data-testid="twofa-code"
              />
              {error ? (
                <p id="twofa-code-error" role="alert" className="text-xs text-danger">
                  {error}
                </p>
              ) : null}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose}>
                {t('profile.twofa.cancel')}
              </Button>
              <Button
                type="submit"
                disabled={!setup.data || enable.isPending}
                data-testid="twofa-verify"
              >
                {enable.isPending
                  ? t('profile.twofa.setup.verifying')
                  : t('profile.twofa.setup.verify')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// Disable: password + code

function DisableDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation('auth')
  const errorText = useTotpErrorText()
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [errors, setErrors] = useState<{ password?: string; code?: string; form?: string }>({})
  const disable = useMutation({
    mutationFn: () => disableTotp(password, code.trim()),
    onSuccess: () => {
      useSessionStore.getState().updateUser({ totpEnabled: false })
      toast.success(t('profile.twofa.toast.disabled'))
      onClose()
    },
    onError: (e) => {
      if (apiFieldError(e, 'password') === 'incorrect')
        setErrors({ password: t('profile.twofa.errors.password') })
      else if (apiFieldError(e, 'code') === 'totp_invalid')
        setErrors({ code: t('profile.twofa.errors.invalidCode') })
      else setErrors({ form: errorText(e) })
    },
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const next: typeof errors = {}
    if (!password) next.password = t('profile.twofa.errors.required')
    if (!code.trim()) next.code = t('profile.twofa.errors.required')
    setErrors(next)
    if (Object.keys(next).length === 0) disable.mutate()
  }
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent data-testid="twofa-disable-dialog">
        <form onSubmit={submit} className="space-y-4" noValidate>
          <DialogHeader>
            <DialogTitle>{t('profile.twofa.disableDialog.title')}</DialogTitle>
            <DialogDescription>{t('profile.twofa.disableDialog.subtitle')}</DialogDescription>
          </DialogHeader>
          <PasswordField
            value={password}
            onChange={setPassword}
            error={errors.password}
            testId="twofa-disable-password"
          />
          <div className="space-y-1.5">
            <Label htmlFor="twofa-disable-code">{t('profile.twofa.disableDialog.code')}</Label>
            <Input
              id="twofa-disable-code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              autoComplete="one-time-code"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={16}
              className="font-mono"
              aria-invalid={errors.code ? true : undefined}
              aria-describedby={errors.code ? 'twofa-disable-code-error' : undefined}
              data-testid="twofa-disable-code"
            />
            {errors.code ? (
              <p id="twofa-disable-code-error" role="alert" className="text-xs text-danger">
                {errors.code}
              </p>
            ) : null}
          </div>
          {errors.form ? (
            <p role="alert" className="text-sm text-danger">
              {errors.form}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('profile.twofa.cancel')}
            </Button>
            <Button
              type="submit"
              variant="destructive"
              disabled={disable.isPending}
              data-testid="twofa-disable-submit"
            >
              {t('profile.twofa.disableDialog.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function PasswordField({
  value,
  onChange,
  error,
  testId,
}: {
  value: string
  onChange: (v: string) => void
  error?: string | undefined
  testId: string
}) {
  const { t } = useTranslation('auth')
  const id = useId()
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{t('profile.twofa.password')}</Label>
      <Input
        id={id}
        type="password"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        autoComplete="current-password"
        autoFocus
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : undefined}
        data-testid={testId}
      />
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-xs text-danger">
          {error}
        </p>
      ) : null}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Regenerate recovery codes: password → codes shown once

function RegenerateDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation('auth')
  const errorText = useTotpErrorText()
  const user = useSessionStore((s) => s.user)
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [codes, setCodes] = useState<string[] | null>(null)
  const [acked, setAcked] = useState(false)
  const regen = useMutation({
    mutationFn: () => regenerateRecoveryCodes(password),
    onSuccess: (res) => setCodes(res.recoveryCodes),
    onError: (e) =>
      setError(
        apiFieldError(e, 'password') === 'incorrect'
          ? t('profile.twofa.errors.password')
          : errorText(e),
      ),
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!password) {
      setError(t('profile.twofa.errors.required'))
      return
    }
    setError(null)
    regen.mutate()
  }
  const canClose = codes === null || acked
  return (
    <Dialog open onOpenChange={(open) => !open && canClose && onClose()}>
      <DialogContent
        data-testid="twofa-regen-dialog"
        onEscapeKeyDown={(e) => !canClose && e.preventDefault()}
        onInteractOutside={(e) => !canClose && e.preventDefault()}
      >
        {codes ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('profile.twofa.codes.title')}</DialogTitle>
              <DialogDescription>{t('profile.twofa.codes.regenerated')}</DialogDescription>
            </DialogHeader>
            <RecoveryCodes
              codes={codes}
              acked={acked}
              onAck={setAcked}
              username={user?.username ?? ''}
            />
            <DialogFooter>
              <Button type="button" disabled={!acked} onClick={onClose} data-testid="twofa-done">
                {t('profile.twofa.codes.done')}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="space-y-4" noValidate>
            <DialogHeader>
              <DialogTitle>{t('profile.twofa.regenDialog.title')}</DialogTitle>
              <DialogDescription>{t('profile.twofa.regenDialog.subtitle')}</DialogDescription>
            </DialogHeader>
            <PasswordField
              value={password}
              onChange={setPassword}
              error={error ?? undefined}
              testId="twofa-regen-password"
            />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose}>
                {t('profile.twofa.cancel')}
              </Button>
              <Button type="submit" disabled={regen.isPending} data-testid="twofa-regen-submit">
                {t('profile.twofa.regenDialog.submit')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
