import { createFileRoute } from '@tanstack/react-router'
import { AuditPage } from '@/features/admin/AuditPage'

/** `/admin/audit` (SPEC §8.17; Step 19b). */
export const Route = createFileRoute('/_app/admin/audit')({
  component: AuditPage,
})
