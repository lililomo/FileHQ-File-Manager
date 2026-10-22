import { createFileRoute } from '@tanstack/react-router'
import { splatToPath } from '@/features/browser/path'
import { SharedPage } from '@/features/share/SharedPage'

export interface SharedSearch {
  /** file open in the viewer, relative to the shared item */
  view?: string
}

/** `/shared-with-me/<shareId>[/sub/path]?view=<file>`: the recipient's
 *  browser inside one received share (SPEC §8.15 "Dibagikan ke saya"). */
export const Route = createFileRoute('/_app/shared-with-me/$shareId/$')({
  validateSearch: (search: Record<string, unknown>): SharedSearch => {
    const out: SharedSearch = {}
    if (typeof search.view === 'string' && search.view.startsWith('/')) out.view = search.view
    return out
  },
  component: SharedRoute,
})

function SharedRoute() {
  const { shareId, _splat } = Route.useParams()
  const { view } = Route.useSearch()
  return <SharedPage shareId={shareId} path={splatToPath(_splat)} viewPath={view} />
}
