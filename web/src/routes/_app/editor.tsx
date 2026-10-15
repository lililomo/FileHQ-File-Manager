import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderPage } from '@/components/PlaceholderPage'

/** `/editor?path=&line=&col=` (SPEC §8.13); the workspace lands in Step 16a.
 *  Search results already link here with the file and line. */
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
  component: () => <PlaceholderPage step="16a" titleKey="editor" />,
})
