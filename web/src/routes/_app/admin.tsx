import { createFileRoute, redirect } from '@tanstack/react-router'
import { AdminLayout } from '@/features/admin/AdminLayout'

/** Admin area: role checked here (the server enforces it too with 403). */
export const Route = createFileRoute('/_app/admin')({
  beforeLoad: ({ context }) => {
    if (context.user.role !== 'admin') throw redirect({ to: '/files/$', params: { _splat: '' } })
  },
  component: AdminLayout,
})
