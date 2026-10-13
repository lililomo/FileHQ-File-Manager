import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'

/** Admin area: role checked here (the server enforces it too). */
export const Route = createFileRoute('/_app/admin')({
  beforeLoad: ({ context }) => {
    if (context.user.role !== 'admin') throw redirect({ to: '/files/$', params: { _splat: '' } })
  },
  component: () => <Outlet />,
})
