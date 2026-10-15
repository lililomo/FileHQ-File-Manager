import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { PageSpinner } from '@/components/PageSpinner'

const EditorWorkspace = lazy(() =>
  import('@/features/editor/EditorWorkspace').then((m) => ({ default: m.EditorWorkspace })),
)

/** `/editor?path=&line=&col=` (SPEC §8.13): the full-page workspace. Monaco
 *  and the workspace are a separate chunk loaded only here. */
export const Route = createFileRoute('/_app/editor')({
  validateSearch: (
    search: Record<string, unknown>,
  ): { path?: string; line?: number; col?: number } => {
    const out: { path?: string; line?: number; col?: number } = {}
    if (typeof search.path === 'string' && search.path.startsWith('/')) out.path = search.path
    if (typeof search.line === 'number' && search.line > 0) out.line = Math.floor(search.line)
    if (typeof search.col === 'number' && search.col > 0) out.col = Math.floor(search.col)
    return out
  },
  component: EditorRoute,
})

function EditorRoute() {
  const { path, line, col } = Route.useSearch()
  return (
    <div className="h-full">
      <Suspense fallback={<PageSpinner />}>
        <EditorWorkspace mode="page" initialPath={path} line={line} col={col} />
      </Suspense>
    </div>
  )
}
