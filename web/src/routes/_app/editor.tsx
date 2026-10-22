import { useQuery } from '@tanstack/react-query'
import { lazy, Suspense, useEffect, useSyncExternalStore } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { PageSpinner } from '@/components/PageSpinner'
import { getSharedHeader } from '@/features/share/sharedApi'
import { getShareScope, onShareScopeChange, setShareScope } from '@/lib/share-scope'

const EditorWorkspace = lazy(() =>
  import('@/features/editor/EditorWorkspace').then((m) => ({ default: m.EditorWorkspace })),
)

/** `/editor?path=&line=&col=` (SPEC §8.13): the full-page workspace. Monaco
 *  and the workspace are a separate chunk loaded only here. */
export const Route = createFileRoute('/_app/editor')({
  validateSearch: (
    search: Record<string, unknown>,
  ): { path?: string; line?: number; col?: number; version?: string; share?: string } => {
    const out: { path?: string; line?: number; col?: number; version?: string; share?: string } = {}
    if (typeof search.path === 'string' && search.path.startsWith('/')) out.path = search.path
    if (typeof search.line === 'number' && search.line > 0) out.line = Math.floor(search.line)
    if (typeof search.col === 'number' && search.col > 0) out.col = Math.floor(search.col)
    if (typeof search.version === 'string' && /^[0-9A-Za-z-]{1,64}$/.test(search.version))
      out.version = search.version
    // Step 18b: a file inside a received share (paths relative to the share)
    if (typeof search.share === 'string' && /^[0-9A-Za-z-]{1,64}$/.test(search.share))
      out.share = search.share
    return out
  },
  component: EditorRoute,
})

function EditorRoute() {
  const { path, line, col, version, share } = Route.useSearch()
  // inside a received share the scope must be active before the workspace
  // loads the file: resolve the header (permission, single file) first
  const header = useQuery({
    queryKey: ['shared-with-me', share ?? ''],
    queryFn: ({ signal }) => getSharedHeader(share ?? '', signal),
    enabled: !!share,
    retry: false,
    staleTime: 30_000,
  })
  const h = share ? header.data : undefined
  const active = useSyncExternalStore(onShareScopeChange, getShareScope, getShareScope)
  useEffect(() => {
    if (!h) {
      setShareScope(null)
      return
    }
    setShareScope({
      shareId: h.shareId,
      permission: h.permission,
      isFile: !h.isDir,
      name: h.name,
      allowDownload: h.allowDownload,
    })
    return () => setShareScope(null)
  }, [h])
  if (share && (header.isPending || (h && active?.shareId !== share))) return <PageSpinner />
  return (
    <div className="h-full">
      <Suspense fallback={<PageSpinner />}>
        <EditorWorkspace
          mode="page"
          initialPath={path}
          line={line}
          col={col}
          initialVersion={version}
          scope={share}
        />
      </Suspense>
    </div>
  )
}
