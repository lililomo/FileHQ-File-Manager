import { createFileRoute } from '@tanstack/react-router'
import { BrowserLayout } from '@/features/browser/BrowserLayout'
import { splatToPath } from '@/features/browser/path'

/** `?select=<name>` selects and reveals one item after the folder loads
 *  ("Buka lokasi file" from Recent / Favorit / Search, SPEC §9.4). */
export const Route = createFileRoute('/_app/files/$')({
  validateSearch: (search: Record<string, unknown>): { select?: string } => {
    const select = typeof search.select === 'string' && search.select ? search.select : undefined
    return select ? { select } : {}
  },
  component: FilesRoute,
})

function FilesRoute() {
  const { _splat } = Route.useParams()
  const { select } = Route.useSearch()
  return <BrowserLayout path={splatToPath(_splat)} selectName={select} />
}
