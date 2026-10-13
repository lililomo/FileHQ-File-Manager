import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/recent')({
  component: () => <PlaceholderPage step="10b" titleKey="recent" />,
})
