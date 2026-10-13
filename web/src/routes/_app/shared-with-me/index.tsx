import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

export const Route = createFileRoute('/_app/shared-with-me/')({
  component: () => <PlaceholderPage step="18b" titleKey="sharedWithMe" />,
})
