import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { configure, render, screen } from '@testing-library/react'

// The memory router renders asynchronously; on a 1 vCPU box the first render can exceed 1 s.
configure({ asyncUtilTimeout: 8000 })
import userEvent from '@testing-library/user-event'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Breadcrumb } from './Breadcrumb'
import { collapseSegments, segmentsOf } from './breadcrumb-utils'

vi.mock('./api', async (orig) => {
  const mod = await orig<typeof import('./api')>()
  return {
    ...mod,
    listDir: vi.fn(async (path: string) => ({
      items: ['alpha', 'beta'].map((n) => ({
        name: n,
        path: `${path === '/' ? '' : path}/${n}`,
        isDir: true,
        type: 'dir',
      })),
      nextCursor: null,
      total: 2,
      path,
      parentPath: null,
      dir: { path, etag: '', mtime: '', itemCount: 2 },
    })),
  }
})

function renderWithRouter(ui: React.ReactNode) {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const index = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <>{ui}</>,
  })
  const files = createRoute({
    getParentRoute: () => rootRoute,
    path: '/files/$',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([index, files]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const qc = new QueryClient()
  return render(
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <RouterProvider router={router} />
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

describe('segments & collapse', () => {
  it('builds segments with the root first', () => {
    expect(segmentsOf('/a/b').map((s) => s.path)).toEqual(['/', '/a', '/a/b'])
    expect(segmentsOf('/').map((s) => s.path)).toEqual(['/'])
  })
  it('collapses middle segments beyond the limit', () => {
    const segs = segmentsOf('/a/b/c/d/e')
    const { head, hidden, tail } = collapseSegments(segs)
    expect(head.map((s) => s.name)).toEqual([''])
    expect(hidden.map((s) => s.name)).toEqual(['a', 'b', 'c'])
    expect(tail.map((s) => s.name)).toEqual(['d', 'e'])
    expect(collapseSegments(segmentsOf('/a/b/c')).hidden).toEqual([])
  })
})

beforeAll(() => i18n.changeLanguage('id'))

describe('<Breadcrumb>', () => {
  it('renders links for ancestors and the current folder as page', async () => {
    renderWithRouter(<Breadcrumb path="/proyek/web" />)
    expect(await screen.findByRole('link', { name: 'File Saya' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'proyek' })).toBeInTheDocument()
    expect(screen.getByText('web')).toHaveAttribute('aria-current', 'page')
  })
  it('shows an ellipsis menu with hidden segments for deep paths', async () => {
    const user = userEvent.setup()
    renderWithRouter(<Breadcrumb path="/a/b/c/d/e" />)
    const ellipsis = await screen.findByTestId('crumb-ellipsis')
    expect(screen.queryByRole('link', { name: 'b' })).not.toBeInTheDocument()
    await user.click(ellipsis)
    expect(await screen.findByRole('menuitem', { name: 'b' })).toBeInTheDocument()
  })
  it('lazily loads sibling folders when a segment menu opens', async () => {
    const user = userEvent.setup()
    const { listDir } = await import('./api')
    renderWithRouter(<Breadcrumb path="/proyek/web" />)
    await screen.findByRole('link', { name: 'proyek' })
    expect(listDir).not.toHaveBeenCalled()
    await user.click(screen.getByTestId('crumb-menu-/proyek/web'))
    expect(await screen.findByRole('menuitem', { name: 'alpha' })).toBeInTheDocument()
    expect(listDir).toHaveBeenCalledWith(
      '/proyek',
      expect.objectContaining({ types: 'folder' }),
      null,
      expect.anything(),
    )
  })
  it('compact mode shows only the last segment', async () => {
    renderWithRouter(<Breadcrumb path="/a/b" compact />)
    expect(await screen.findByText('b')).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByText('a')).not.toBeInTheDocument()
  })
})
