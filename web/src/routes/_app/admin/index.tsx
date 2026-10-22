import { createFileRoute } from '@tanstack/react-router'
import { Dashboard } from '@/features/admin/Dashboard'

/** `/admin`: the dashboard (SPEC §8.17). */
export const Route = createFileRoute('/_app/admin/')({
  component: Dashboard,
})
