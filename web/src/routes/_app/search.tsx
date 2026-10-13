import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/search')({
  component: () => <PlaceholderPage step="15" titleKey="search" />,
})
