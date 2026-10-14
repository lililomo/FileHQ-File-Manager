import { createFileRoute } from '@tanstack/react-router'
import { RecentPage } from '@/features/recent/RecentPage'

export const Route = createFileRoute('/_app/recent')({
  component: RecentPage,
})
