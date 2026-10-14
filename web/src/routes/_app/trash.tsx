import { createFileRoute } from '@tanstack/react-router'
import { TrashPage } from '@/features/trash/TrashPage'

export const Route = createFileRoute('/_app/trash')({
  component: TrashPage,
})
