import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/admin/shares')({
  component: () => <PlaceholderPage step="19b" titleKey="adminShares" />,
})
