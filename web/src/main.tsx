import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import '@/styles/globals.css'
import '@/i18n'
import { routeTree } from './routeTree.gen'
import { queryClient } from '@/lib/query'
import { setApiHandlers } from '@/lib/api'
import { watchSystemTheme } from '@/lib/theme'
import { useUIStore } from '@/stores/ui'

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: 'intent',
  defaultPreloadStaleTime: 0,
  scrollRestoration: true,
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

setApiHandlers({
  onUnauthorized: () => {
    const here = window.location.pathname + window.location.search
    if (here.startsWith('/login') || here.startsWith('/s/')) return
    void router.navigate({ to: '/login', search: { next: here } })
  },
  onPasswordChangeRequired: () => {
    if (window.location.pathname === '/change-password') return
    void router.navigate({ to: '/change-password' })
  },
})

watchSystemTheme((resolved) => useUIStore.getState().setResolvedTheme(resolved))

const container = document.getElementById('root')
if (!container) {
  throw new Error('#root not found')
}
createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
