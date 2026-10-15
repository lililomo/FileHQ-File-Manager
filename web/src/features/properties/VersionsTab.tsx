import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import type { Item } from '@/features/browser/api'
import { useVersionActions } from '@/features/editor/useVersionActions'
import { VersionList } from '@/features/editor/VersionHistory'
import { listVersions, versionsQueryKey } from '@/features/editor/versionsApi'

/** Properties → "Riwayat versi" (SPEC §8.13): list, open read-only in the
 *  editor, restore / download / delete. Compare lives in the editor. */
export function VersionsTab({ item, onClose }: { item: Item; onClose: () => void }) {
  const { t } = useTranslation('files')
  const navigate = useNavigate()
  const q = useQuery({
    queryKey: versionsQueryKey(item.path),
    queryFn: ({ signal }) => listVersions(item.path, signal),
    enabled: !item.isDir,
  })
  const actions = useVersionActions(item.path)
  if (item.isDir) return <p className="text-sm text-text-muted">{t('props.versionsHint')}</p>
  return (
    <div className="flex flex-col gap-2" data-testid="props-versions">
      <p className="text-xs text-text-muted">{t('props.versionsHint')}</p>
      <VersionList
        path={item.path}
        data={q.data}
        loading={q.isPending}
        error={q.error}
        onOpen={(v) => {
          onClose()
          void navigate({ to: '/editor', search: { path: item.path, version: v.id } })
        }}
        onRestore={(v) => actions.confirm({ kind: 'restore', v })}
        onDelete={(v) => actions.confirm({ kind: 'delete', v })}
      />
      {actions.dialog}
    </div>
  )
}
