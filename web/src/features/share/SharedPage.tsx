import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Users } from 'lucide-react'
import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/EmptyState'
import { PageSpinner } from '@/components/PageSpinner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { pathToSplat } from '@/features/browser/path'
import { errorMessage } from '@/lib/errors'
import { isApiError } from '@/lib/api'
import { getShareScope, onShareScopeChange, setShareScope } from '@/lib/share-scope'
import { SharedBrowser } from './SharedBrowser'
import { getSharedHeader, sharedHeaderKey } from './sharedApi'

export interface SharedPageProps {
  shareId: string
  /** folder inside the share ("/" = the shared item) */
  path: string
  viewPath?: string | undefined
}

/** `/shared-with-me/<id>/*` (SPEC §8.15 "Dibagikan ke saya"): the recipient's
 *  browser of one share. The share scope is active while mounted so the
 *  file, viewer, editor, upload and download helpers call the session
 *  endpoints with `?share=`; paths are relative to the shared item. */
export function SharedPage({ shareId, path, viewPath }: SharedPageProps) {
  const { t } = useTranslation(['share', 'nav'])
  const navigate = useNavigate()
  const header = useQuery({
    queryKey: sharedHeaderKey(shareId),
    queryFn: ({ signal }) => getSharedHeader(shareId, signal),
    retry: false,
    staleTime: 30_000,
  })
  const h = header.data
  // the browser mounts only once the scope is active, so its first requests
  // already carry ?share= (the scope lives outside React)
  const active = useSyncExternalStore(onShareScopeChange, getShareScope, getShareScope)
  useEffect(() => {
    if (!h) return
    setShareScope({
      shareId: h.shareId,
      permission: h.permission,
      isFile: !h.isDir,
      name: h.name,
      allowDownload: h.allowDownload,
    })
    return () => setShareScope(null)
  }, [h])
  const onNavigate = useCallback(
    (folder: string, view?: string) => {
      void navigate({
        to: '/shared-with-me/$shareId/$',
        params: { shareId, _splat: pathToSplat(folder) },
        search: view ? { view } : {},
        replace: !!view && !!viewPath,
      })
    },
    [navigate, shareId, viewPath],
  )
  if (header.isPending) return <PageSpinner />
  if (header.isError) {
    const notFound = isApiError(header.error, 'NOT_FOUND')
    const gone =
      isApiError(header.error, 'SHARE_DISABLED') || isApiError(header.error, 'SHARE_EXPIRED')
    return (
      <EmptyState
        icon={Users}
        title={
          notFound
            ? t('share:shared.notFound')
            : gone
              ? t('share:shared.unavailable')
              : t('share:public.error')
        }
        description={notFound ? t('share:shared.notFoundBody') : errorMessage(header.error)}
        className="h-full"
        action={
          <Button variant="outline" onClick={() => void navigate({ to: '/shared-with-me' })}>
            {t('nav:sharedWithMe')}
          </Button>
        }
      />
    )
  }
  return (
    <div className="flex h-full flex-col" data-testid="shared-page" data-permission={h!.permission}>
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="truncate text-lg font-semibold" data-testid="shared-title">
          {h!.name}
        </h1>
        <span className="text-xs text-text-muted">
          {t('share:shared.by', { name: h!.owner.displayName || h!.owner.username })}
        </span>
        <Badge
          variant={h!.permission === 'editor' ? 'default' : 'secondary'}
          data-testid="shared-page-perm"
        >
          {h!.permission === 'editor' ? t('share:shared.editorBadge') : t('share:shared.readOnly')}
        </Badge>
        <span className="flex-1" />
        <Button variant="ghost" size="sm" onClick={() => void navigate({ to: '/shared-with-me' })}>
          {t('nav:sharedWithMe')}
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-auto p-3">
        {active?.shareId === shareId ? (
          <SharedBrowser header={h!} path={path} viewPath={viewPath} onNavigate={onNavigate} />
        ) : (
          <PageSpinner />
        )}
      </div>
    </div>
  )
}
