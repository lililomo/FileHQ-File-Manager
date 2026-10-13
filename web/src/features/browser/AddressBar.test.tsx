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
import { useState } from 'react'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AddressBar } from './AddressBar'

function Harness({
  onNavigate,
  initialEditing = false,
}: {
  onNavigate: (p: string) => void
  initialEditing?: boolean
}) {
  const [editing, setEditing] = useState(initialEditing)
  return (
    <AddressBar
      path="/proyek"
      canBack
      canForward={false}
      canUp
      onBack={() => {}}
      onForward={() => {}}
      onUp={() => {}}
      onRefresh={() => {}}
      onNavigate={onNavigate}
      editing={editing}
      onEditingChange={setEditing}
    />
  )
}

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
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <TooltipProvider>
        <RouterProvider router={router} />
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

beforeAll(() => i18n.changeLanguage('id'))

describe('<AddressBar>', () => {
  it('enters typing mode, validates, and navigates on Enter', async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    renderWithRouter(<Harness onNavigate={onNavigate} />)
    await user.click(await screen.findByTestId('breadcrumb-edit'))
    const input = await screen.findByTestId('path-input')
    expect(input).toHaveValue('/proyek')
    await user.clear(input)
    await user.type(input, '/a/../b{Enter}')
    expect(screen.getByRole('alert')).toHaveTextContent('Path tidak valid')
    expect(onNavigate).not.toHaveBeenCalled()
    await user.clear(input)
    await user.type(input, 'sub//dir/{Enter}')
    expect(onNavigate).toHaveBeenCalledWith('/proyek/sub/dir')
    expect(screen.queryByTestId('path-input')).not.toBeInTheDocument()
  })
  it('Escape cancels typing mode without navigating', async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    renderWithRouter(<Harness onNavigate={onNavigate} initialEditing />)
    const input = await screen.findByTestId('path-input')
    await user.type(input, 'x{Escape}')
    expect(onNavigate).not.toHaveBeenCalled()
    expect(screen.queryByTestId('path-input')).not.toBeInTheDocument()
    expect(screen.getByTestId('breadcrumb')).toBeInTheDocument()
  })
  it('disables forward when there is no forward history', async () => {
    renderWithRouter(<Harness onNavigate={() => {}} />)
    expect(await screen.findByTestId('nav-forward')).toBeDisabled()
    expect(screen.getByTestId('nav-back')).toBeEnabled()
  })
})
