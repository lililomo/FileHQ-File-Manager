import { createFileRoute, redirect } from '@tanstack/react-router'
import { LoginPage } from '@/features/auth/LoginPage'
import { ensureSession } from '@/features/auth/session'

/** Only same-origin absolute paths are accepted as `next` (no open redirect). */
function safeNext(v: unknown): string | undefined {
  return typeof v === 'string' &&
    v.startsWith('/') &&
    !v.startsWith('//') &&
    !v.startsWith('/login')
    ? v
    : undefined
}

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>) => ({ next: safeNext(search.next) }),
  beforeLoad: async ({ search }) => {
    const user = await ensureSession()
    if (user?.mustChangePassword) throw redirect({ to: '/change-password' })
    if (user) {
      if (search.next) throw redirect({ href: search.next })
      throw redirect({ to: '/files/$', params: { _splat: '' } })
    }
  },
  component: LoginRoute,
})

function LoginRoute() {
  const { next } = Route.useSearch()
  return <LoginPage next={next} />
}
