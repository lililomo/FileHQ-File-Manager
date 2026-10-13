import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/editor')({
  component: () => <PlaceholderPage step="16a" titleKey="editor" />,
})
