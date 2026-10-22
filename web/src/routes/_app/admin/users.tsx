import { createFileRoute } from '@tanstack/react-router'
import { UsersPage } from '@/features/admin/UsersPage'

/** `/admin/users`: account management (SPEC §8.17). */
export const Route = createFileRoute('/_app/admin/users')({
  component: UsersPage,
})
