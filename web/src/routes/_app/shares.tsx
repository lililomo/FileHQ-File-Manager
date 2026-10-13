import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/shares')({
  component: () => <PlaceholderPage step="18b" titleKey="myShares" />,
})
