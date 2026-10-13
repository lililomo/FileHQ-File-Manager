import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { useSessionStore } from '@/stores/session'
import { AuthCard } from './AuthCard'
import { LoginForm } from './LoginForm'
import { applyUserPrefs } from './session'
import type { LoginResponse } from './api'

export function LoginPage({ next }: { next: string | undefined }) {
  const { t } = useTranslation('auth')
  const navigate = useNavigate()
  const onSuccess = (res: LoginResponse) => {
    useSessionStore.getState().setAuthenticated(res.user, null, res.csrfToken)
    applyUserPrefs(res.user)
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
