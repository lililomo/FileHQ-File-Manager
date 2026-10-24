import type { QueryClient } from '@tanstack/react-query'
import { Outlet, createRootRouteWithContext } from '@tanstack/react-router'
import { Suspense, lazy } from 'react'
import { FileIconSprite } from '@/components/file-icon/FileIcon'
import { LiveRegion } from '@/components/LiveRegion'
import { NotFoundPage } from '@/components/NotFoundPage'
import { PageSpinner } from '@/components/PageSpinner'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'

const RouterDevtools = import.meta.env.DEV
  ? lazy(() =>
      import('@tanstack/react-router-devtools').then((m) => ({
        default: m.TanStackRouterDevtools,
      })),
    )
  : null

export interface RouterContext {
  queryClient: QueryClient
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootComponent,
  notFoundComponent: NotFoundPage,
  pendingComponent: PageSpinner,
})

function RootComponent() {
  return (
    <TooltipProvider delayDuration={300}>
      <Outlet />
      <Toaster />
      <LiveRegion />
      <FileIconSprite />
      {RouterDevtools ? (
        <Suspense fallback={null}>
          <RouterDevtools position="bottom-right" />
        </Suspense>
      ) : null}
    </TooltipProvider>
  )
}
