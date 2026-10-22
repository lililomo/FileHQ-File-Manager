import { createFileRoute } from '@tanstack/react-router'
import { SettingsPage } from '@/features/admin/SettingsPage'

/** `/admin/settings`: live overrides of the environment (SPEC §8.17, §3.1). */
export const Route = createFileRoute('/_app/admin/settings')({
  component: SettingsPage,
})
