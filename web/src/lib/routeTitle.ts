import { useRouterState } from '@tanstack/react-router'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { useSessionStore } from '@/stores/session'

/** First path segment → nav label key (SPEC §9.9, WCAG 2.4.2 "Page Titled"). */
const SECTION: Record<string, string> = {
  files: 'files',
  search: 'search',
  recent: 'recent',
  favorites: 'favorites',
  'shared-with-me': 'sharedWithMe',
  shares: 'myShares',
  trash: 'trash',
  activity: 'activity',
  storage: 'storage',
  editor: 'editor',
  settings: 'settings',
  admin: 'admin',
}
const ADMIN: Record<string, string> = {
  users: 'adminUsers',
  settings: 'adminSettings',
  audit: 'adminAudit',
  tasks: 'adminTasks',
  maintenance: 'adminMaintenance',
  shares: 'adminShares',
}

/** Pure part of useRouteTitle: the title parts for a pathname. */
export function titleParts(pathname: string, t: (key: string) => string): string[] {
  const segs = pathname.split('/').filter(Boolean).map(decodeURIComponent)
  const key = SECTION[segs[0] ?? '']
  if (!key) return []
  if (key === 'files' && segs.length > 1) return [segs[segs.length - 1]!, t('nav:files')]
  if (key === 'admin' && segs[1] && ADMIN[segs[1]])
    return [t(`nav:${ADMIN[segs[1]]}`), t('nav:admin')]
  return [t(`nav:${key}`)]
}

/** Keeps document.title in step with the route: "<folder> · File Saya · <app>". */
export function useRouteTitle(): void {
  const { t } = useTranslation(['nav', 'common'])
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const appName = useSessionStore((s) => s.config?.appName) || t('common:app.name')
  useEffect(() => {
    // public share pages and login set nothing special: app name only
    const parts = titleParts(pathname, t as unknown as (key: string) => string)
    document.title = [...parts, appName].join(' · ')
  }, [pathname, appName, t])
}
