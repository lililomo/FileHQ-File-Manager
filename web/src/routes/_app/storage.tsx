import { createFileRoute } from '@tanstack/react-router'
import { StoragePage } from '@/features/storage-analysis/StoragePage'
import { parseStorageSearch } from '@/features/storage-analysis/dupSelection'

/** `/storage?tab=duplicates` (SPEC §8.7): summary or duplicate finder tab. */
export const Route = createFileRoute('/_app/storage')({
  validateSearch: parseStorageSearch,
  component: StorageRoute,
})

function StorageRoute() {
  const { tab } = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <StoragePage
      tab={tab ?? 'summary'}
      onTabChange={(next) =>
        void navigate({ search: next === 'duplicates' ? { tab: 'duplicates' } : {}, replace: true })
      }
    />
  )
}
