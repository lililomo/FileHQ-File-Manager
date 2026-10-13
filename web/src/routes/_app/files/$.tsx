import { createFileRoute } from '@tanstack/react-router'
import { BrowserLayout } from '@/features/browser/BrowserLayout'
import { splatToPath } from '@/features/browser/path'

export const Route = createFileRoute('/_app/files/$')({
  component: FilesRoute,
})

function FilesRoute() {
  const { _splat } = Route.useParams()
  return <BrowserLayout path={splatToPath(_splat)} />
}
