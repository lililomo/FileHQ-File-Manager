import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/admin/users')({
  component: () => <PlaceholderPage step="19a" titleKey="adminUsers" />,
})
