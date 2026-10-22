import { createFileRoute } from '@tanstack/react-router'
import { AdminBrowsePage } from '@/features/admin/AdminBrowsePage'
import { splatToPath } from '@/features/browser/path'

export interface AdminBrowseSearch {
  view?: string
}

/** `/admin/users/<username>/files/*?view=<file>`: an admin inside another
 *  account's root (ADMIN_CAN_BROWSE_USERS; SPEC §8.1 "Admin browse user"). */
export const Route = createFileRoute('/_app/admin/users_/$username/files/$')({
  validateSearch: (search: Record<string, unknown>): AdminBrowseSearch => {
    const out: AdminBrowseSearch = {}
    if (typeof search.view === 'string' && search.view.startsWith('/')) out.view = search.view
    return out
  },
  component: AdminBrowseRoute,
})

function AdminBrowseRoute() {
  const { username, _splat } = Route.useParams()
  const { view } = Route.useSearch()
  return <AdminBrowsePage username={username} path={splatToPath(_splat)} viewPath={view} />
}
