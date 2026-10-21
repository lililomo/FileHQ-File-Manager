import { createFileRoute } from '@tanstack/react-router'
import { splatToPath } from '@/features/browser/path'
import { PublicPage } from '@/features/public/PublicPage'

export interface PublicSearch {
  /** file open in the viewer, relative to the shared item */
  view?: string
}

/** Public share page `/s/<token>[/sub/path]?view=<file>` (SPEC §8.15; no
 *  session). `<token>` is a 43-char token or a custom slug. */
export const Route = createFileRoute('/s/$token/$')({
  validateSearch: (search: Record<string, unknown>): PublicSearch => {
    const out: PublicSearch = {}
    if (typeof search.view === 'string' && search.view.startsWith('/')) out.view = search.view
    return out
  },
  component: PublicRoute,
})

function PublicRoute() {
  const { token, _splat } = Route.useParams()
  const { view } = Route.useSearch()
  return <PublicPage token={token} path={splatToPath(_splat)} viewPath={view} />
}
