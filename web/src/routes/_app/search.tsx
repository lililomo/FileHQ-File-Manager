import { createFileRoute } from '@tanstack/react-router'
import { SearchPage, type SearchParams } from '@/features/search/SearchPage'

/** `/search?q=&path=&kind=` (SPEC §9.3): deep-linkable name/content search. */
export const Route = createFileRoute('/_app/search')({
  validateSearch: (search: Record<string, unknown>): SearchParams => {
    const out: SearchParams = {}
    if (typeof search.q === 'string' && search.q) out.q = search.q
    if (typeof search.path === 'string' && search.path.startsWith('/')) out.path = search.path
    if (search.kind === 'content' || search.kind === 'name') out.kind = search.kind
    return out
  },
  component: SearchRoute,
})

function SearchRoute() {
  const params = Route.useSearch()
  return (
    <SearchPage
      key={`${params.kind ?? ''}|${params.q ?? ''}|${params.path ?? ''}`}
      params={params}
    />
  )
}
