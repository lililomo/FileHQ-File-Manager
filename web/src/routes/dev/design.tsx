import { createFileRoute } from '@tanstack/react-router'
import { Suspense, lazy } from 'react'
import { NotFoundPage } from '@/components/NotFoundPage'
import { PageSpinner } from '@/components/PageSpinner'

// __DESIGN_PAGE__ is replaced at build time (vite `define`); in production
// builds the branch is dead code and the gallery chunk is never emitted.
const DesignSystemPage = __DESIGN_PAGE__ ? lazy(() => import('./-DesignSystemPage')) : null

export const Route = createFileRoute('/dev/design')({
  component: () =>
    DesignSystemPage ? (
      <Suspense fallback={<PageSpinner />}>
        <DesignSystemPage />
      </Suspense>
    ) : (
      <NotFoundPage />
    ),
})
