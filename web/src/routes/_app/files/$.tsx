import { createFileRoute } from '@tanstack/react-router'
import { FilesBrowser } from '@/features/browser/DualPane'
import { splatToPath } from '@/features/browser/path'

export interface FilesSearch {
  select?: string
  archive?: string
  view?: string
}

/** `?select=<name>` selects and reveals one item after the folder loads
 *  ("Buka lokasi file" from Recent / Favorit / Search, SPEC §9.4);
 *  `?archive=<path>` opens the archive browser overlay (SPEC §8.11);
 *  `?view=<path>` opens the viewer overlay on that file (SPEC §8.14 deep link). */
export const Route = createFileRoute('/_app/files/$')({
  validateSearch: (search: Record<string, unknown>): FilesSearch => {
    const out: FilesSearch = {}
    if (typeof search.select === 'string' && search.select) out.select = search.select
    if (typeof search.archive === 'string' && search.archive.startsWith('/'))
      out.archive = search.archive
    if (typeof search.view === 'string' && search.view.startsWith('/') && search.view !== '/')
      out.view = search.view
    return out
  },
  component: FilesRoute,
})

function FilesRoute() {
  const { _splat } = Route.useParams()
  const { select, archive, view } = Route.useSearch()
  return (
    <FilesBrowser
      path={splatToPath(_splat)}
      selectName={select}
      archivePath={archive}
      viewPath={view}
    />
  )
}
