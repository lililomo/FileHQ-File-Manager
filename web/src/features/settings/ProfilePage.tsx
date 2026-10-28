import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { ChangePasswordForm } from '@/features/auth/ChangePasswordForm'
import { useSessionStore } from '@/stores/session'
import { PreferencesForm } from './PreferencesForm'
import { sessionsKey } from './api'
import { SessionsList } from './SessionsList'
import { TwoFactor } from './TwoFactor'

/** `/settings` (SPEC §8.18): password change (other sessions are revoked by
 *  the server), active sessions, 2FA TOTP (Step 23a) and preferences. */
export function ProfilePage() {
  const { t } = useTranslation('auth')
  const qc = useQueryClient()
  const user = useSessionStore((s) => s.user)
  const minLength = useSessionStore((s) => s.config?.passwordMinLength) ?? 12
  return (
    <div className="mx-auto max-w-3xl space-y-8 p-4 md:p-6" data-testid="profile-page">
      <header>
        <h1 className="text-xl font-semibold">{t('profile.title')}</h1>
        <p className="text-sm text-text-muted">
          {user?.displayName || user?.username}
          {user?.displayName ? ` (${user.username})` : ''}
        </p>
      </header>
      <section aria-labelledby="pw-title" className="space-y-3">
        <h2 id="pw-title" className="text-base font-semibold">
          {t('profile.password')}
        </h2>
        <p className="text-xs text-text-muted">{t('profile.passwordHint')}</p>
        <div className="max-w-md">
          <ChangePasswordForm
            key={user?.id}
            minLength={minLength}
            username={user?.username ?? ''}
            onSuccess={() => {
              toast.success(t('changePassword.success'))
              void qc.invalidateQueries({ queryKey: sessionsKey })
            }}
          />
        </div>
      </section>
      <TwoFactor />
      <SessionsList />
      <PreferencesForm />
    </div>
  )
}
