import { createFileRoute } from '@tanstack/react-router'
import { MySharesPage } from '@/features/share/MySharesPage'

/** `/shares` ("Share saya"): every share of the user with its stats and actions (SPEC §8.15). */
export const Route = createFileRoute('/_app/shares')({
  component: MySharesPage,
})
