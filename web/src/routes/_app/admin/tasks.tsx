import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/admin/tasks')({
  component: () => <PlaceholderPage step="19b" titleKey="adminTasks" />,
})
