import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/files/$')({
  component: () => <PlaceholderPage step="7a" titleKey="files" />,
})
