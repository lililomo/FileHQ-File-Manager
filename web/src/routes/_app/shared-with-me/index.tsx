import { createFileRoute } from '@tanstack/react-router'
import { SharedWithMePage } from '@/features/share/SharedWithMePage'

/** `/shared-with-me`: shares received by the user, grouped per owner (SPEC §8.15). */
export const Route = createFileRoute('/_app/shared-with-me/')({
  component: SharedWithMePage,
})
