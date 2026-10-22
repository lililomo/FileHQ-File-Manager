import { createFileRoute } from '@tanstack/react-router'
import { ProfilePage } from '@/features/settings/ProfilePage'

/** `/settings`: profile, sessions and preferences (SPEC §8.18; Step 19b). */
export const Route = createFileRoute('/_app/settings')({
  component: ProfilePage,
})
