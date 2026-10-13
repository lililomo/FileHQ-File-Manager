import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/trash')({
  component: () => <PlaceholderPage step="10a" titleKey="trash" />,
})
