import { createFileRoute, redirect } from '@tanstack/react-router'
import { ChangePasswordPage } from '@/features/auth/ChangePasswordPage'
import { ensureSession } from '@/features/auth/session'

export const Route = createFileRoute('/change-password')({
  beforeLoad: async () => {
    const user = await ensureSession()
    if (!user) throw redirect({ to: '/login', search: { next: '/change-password' } })
  },
  component: ChangePasswordPage,
})
