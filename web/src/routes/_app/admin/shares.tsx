import { createFileRoute } from '@tanstack/react-router'
import { SharesPage } from '@/features/admin/SharesPage'

/** `/admin/shares` (SPEC §8.17; Step 19b). */
export const Route = createFileRoute('/_app/admin/shares')({
  component: SharesPage,
})
