import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { useSessionStore } from '@/stores/session'
import { AuthCard } from './AuthCard'
import { LoginForm } from './LoginForm'
import { applyUserPrefs, ensureSession } from './session'
import type { LoginResponse } from './api'

export function LoginPage({ next }: { next: string | undefined }) {
  const { t } = useTranslation('auth')
  const navigate = useNavigate()
  const onSuccess = (res: LoginResponse) => {
    useSessionStore.getState().setAuthenticated(res.user, null, res.csrfToken)
    applyUserPrefs(res.user)
    // the login answer has no `config` (limits, feature flags such as
    // transcodeEnabled): load it now instead of after the next reload
    // (Step 23d; before, an in-app login kept the defaults until a reload)
    void ensureSession(true).catch(() => undefined)
    if (res.mustChangePassword) {
      void navigate({ to: '/change-password' })
    } else if (next) {
      void navigate({ href: next })
    } else {
      void navigate({ to: '/files/$', params: { _splat: '' } })
    }
  }
  return (
    <AuthCard title={t('login.title')} subtitle={t('login.subtitle')}>
      <LoginForm onSuccess={onSuccess} />
    </AuthCard>
  )
}
