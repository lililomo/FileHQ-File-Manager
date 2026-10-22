import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowLeft, ShieldAlert } from 'lucide-react'
import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import { PageSpinner } from '@/components/PageSpinner'
import { Button } from '@/components/ui/button'
import { pathToSplat } from '@/features/browser/path'
import { SharedBrowser } from '@/features/share/SharedBrowser'
import type { SharedHeader } from '@/features/share/sharedApi'
import { getShareScope, onShareScopeChange, setShareScope } from '@/lib/share-scope'

export interface AdminBrowsePageProps {
  username: string
  path: string
  viewPath?: string | undefined
}

/** `/admin/users/<username>/files/*` (SPEC §8.1 "Admin browse user",
 *  §3.3 ADMIN_CAN_BROWSE_USERS): the admin reads another account's root
 *  through the `?as_user=` scope. The red banner is mandatory and every
 *  request is audited server-side. Read-only in the UI (download allowed);
 *  the browser component is the recipient browser in viewer mode. */
export function AdminBrowsePage({ username, path, viewPath }: AdminBrowsePageProps) {
  const { t } = useTranslation('admin')
  const navigate = useNavigate()
  const header: SharedHeader = {
    shareId: `as_user:${username}`,
    name: username,
    isDir: true,
    permission: 'viewer',
    note: null,
    expiresAt: null,
    allowReshare: false,
    allowDownload: true,
    owner: { username, displayName: username },
  }
  const active = useSyncExternalStore(onShareScopeChange, getShareScope, getShareScope)
  useEffect(() => {
    setShareScope({
      shareId: header.shareId,
      permission: 'viewer',
      isFile: false,
      name: username,
      allowDownload: true,
      asUser: username,
    })
    return () => setShareScope(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [username])
  const onNavigate = useCallback(
    (folder: string, view?: string) => {
      void navigate({
        to: '/admin/users/$username/files/$',
        params: { username, _splat: pathToSplat(folder) },
        search: view ? { view } : {},
        replace: !!view && !!viewPath,
      })
    },
    [navigate, username, viewPath],
  )
  return (
    <div className="flex h-full flex-col" data-testid="admin-browse">
      <div
        role="alert"
        className="flex items-center gap-2 border-b border-danger/40 bg-danger/10 px-3 py-2 text-sm text-text"
        data-testid="admin-browse-banner"
      >
        <ShieldAlert className="size-4 shrink-0 text-danger" aria-hidden="true" />
        <span className="flex-1">{t('browse.banner', { username })}</span>
        <Button asChild variant="outline" size="sm">
          <Link to="/admin/users">
            <ArrowLeft className="size-4" aria-hidden="true" />
            {t('browse.back')}
          </Link>
        </Button>
      </div>
      <div className="min-h-0 flex-1">
        {active?.asUser === username ? (
          <SharedBrowser header={header} path={path} viewPath={viewPath} onNavigate={onNavigate} />
        ) : (
          <PageSpinner />
        )}
      </div>
    </div>
  )
}
