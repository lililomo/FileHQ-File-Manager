import { createFileRoute } from '@tanstack/react-router'
import { MaintenancePage } from '@/features/admin/MaintenancePage'

/** `/admin/maintenance` (SPEC §8.17; Step 19b). */
export const Route = createFileRoute('/_app/admin/maintenance')({
  component: MaintenancePage,
})
