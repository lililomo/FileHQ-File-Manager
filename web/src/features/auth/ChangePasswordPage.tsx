import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useSessionStore } from '@/stores/session'
import { AuthCard } from './AuthCard'
import { ChangePasswordForm } from './ChangePasswordForm'
import { ensureSession } from './session'

export function ChangePasswordPage() {
  const { t } = useTranslation('auth')
  const navigate = useNavigate()
  const user = useSessionStore((s) => s.user)
  const minLength = useSessionStore((s) => s.config?.passwordMinLength) ?? 12
  const onSuccess = async () => {
    useSessionStore.getState().updateUser({ mustChangePassword: false })
    await ensureSession(true)
    toast.success(t('changePassword.success'))
    void navigate({ to: '/files/$', params: { _splat: '' } })
  }
  return (
    <AuthCard
      title={t('changePassword.title')}
      subtitle={
        user?.mustChangePassword
          ? t('changePassword.subtitleRequired')
          : t('changePassword.subtitle')
      }
    >
      <ChangePasswordForm
        minLength={minLength}
        username={user?.username ?? ''}
        onSuccess={onSuccess}
      />
    </AuthCard>
  )
}
