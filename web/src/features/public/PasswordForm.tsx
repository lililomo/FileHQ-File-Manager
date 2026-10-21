import { Lock } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { isApiError } from '@/lib/api'
import { errorMessage } from '@/lib/errors'
import { unlockPublic } from './publicApi'

/** Password gate of a protected link (SPEC §8.15 "state: password"): wrong
 *  password and rate limit are shown inline; success sets the unlock cookie
 *  and the caller refetches the meta. */
export function PasswordForm({ token, onUnlocked }: { token: string; onUnlocked: () => void }) {
  const { t } = useTranslation('share')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await unlockPublic(token, password)
      setPassword('')
      onUnlocked()
    } catch (err) {
      if (isApiError(err, 'SHARE_PASSWORD_WRONG')) setError(t('public.wrongPassword'))
      else if (isApiError(err, 'RATE_LIMITED'))
        setError(t('public.rateLimited', { seconds: Number(err.details?.retryAfter ?? 60) }))
      else setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      onSubmit={submit}
      className="mx-auto mt-8 w-full max-w-sm space-y-3 rounded-lg border border-border bg-surface p-4"
      data-testid="public-password-form"
    >
      <div className="flex items-center gap-2">
        <Lock className="size-5 text-text-muted" aria-hidden="true" />
        <h1 className="text-base font-semibold">{t('public.lockedTitle')}</h1>
      </div>
      <p className="text-sm text-text-muted">{t('public.lockedBody')}</p>
      <div className="space-y-1">
        <Label htmlFor="public-password">{t('public.password')}</Label>
        <Input
          id="public-password"
          type="password"
          autoComplete="current-password"
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-invalid={!!error}
          aria-describedby={error ? 'public-password-error' : undefined}
          data-testid="public-password"
        />
      </div>
      {error ? (
        <p id="public-password-error" role="alert" className="text-sm text-danger">
          {error}
        </p>
      ) : null}
      <Button type="submit" disabled={busy || !password} className="w-full" data-testid="public-unlock">
        {busy ? t('public.unlocking') : t('public.unlock')}
      </Button>
    </form>
  )
}
