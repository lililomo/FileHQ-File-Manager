import { createFileRoute } from '@tanstack/react-router'
import { TasksPage } from '@/features/admin/TasksPage'

/** `/admin/tasks` (SPEC §8.17; Step 19b). */
export const Route = createFileRoute('/_app/admin/tasks')({
  component: TasksPage,
})
