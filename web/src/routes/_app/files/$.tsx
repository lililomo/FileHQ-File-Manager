import { createFileRoute } from '@tanstack/react-router'
import { BrowserLayout } from '@/features/browser/BrowserLayout'
import { splatToPath } from '@/features/browser/path'

/** `?select=<name>` selects and reveals one item after the folder loads
 *  ("Buka lokasi file" from Recent / Favorit / Search, SPEC §9.4);
 *  `?archive=<path>` opens the archive browser overlay (SPEC §8.11). */
export const Route = createFileRoute('/_app/files/$')({
  validateSearch: (search: Record<string, unknown>): { select?: string; archive?: string } => {
    const out: { select?: string; archive?: string } = {}
    if (typeof search.select === 'string' && search.select) out.select = search.select
    if (typeof search.archive === 'string' && search.archive.startsWith('/'))
      out.archive = search.archive
    return out
  },
  component: FilesRoute,
})

function FilesRoute() {
  const { _splat } = Route.useParams()
  const { select, archive } = Route.useSearch()
  return <BrowserLayout path={splatToPath(_splat)} selectName={select} archivePath={archive} />
}
