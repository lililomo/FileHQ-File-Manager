import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'
import { AppShell } from '@/components/shell/AppShell'
import { ensureSession } from '@/features/auth/session'

/** Authenticated layout: session required and password already changed (SPEC §9.4 "S+P"). */
export const Route = createFileRoute('/_app')({
  beforeLoad: async ({ location }) => {
    const user = await ensureSession()
    if (!user) throw redirect({ to: '/login', search: { next: location.href } })
    if (user.mustChangePassword) throw redirect({ to: '/change-password' })
    return { user }
  },
  component: () => (
    <AppShell>
      <Outlet />
    </AppShell>
  ),
})
