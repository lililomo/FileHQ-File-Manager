import { Link, Outlet, type LinkProps } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

const NAV: {
  key: 'dashboard' | 'users' | 'settings' | 'audit' | 'tasks' | 'shares' | 'maintenance'
  link: LinkProps
  exact?: boolean
}[] = [
  { key: 'dashboard', link: { to: '/admin' }, exact: true },
  { key: 'users', link: { to: '/admin/users' } },
  { key: 'settings', link: { to: '/admin/settings' } },
  { key: 'audit', link: { to: '/admin/audit' } },
  { key: 'tasks', link: { to: '/admin/tasks' } },
  { key: 'shares', link: { to: '/admin/shares' } },
  { key: 'maintenance', link: { to: '/admin/maintenance' } },
]

/** `/admin/*` (SPEC §8.17, §9.4): dense sub-navigation above the admin pages.
 *  The role guard lives in the route (`beforeLoad`) and on the server. */
export function AdminLayout() {
  const { t } = useTranslation('admin')
  return (
    <div className="flex h-full flex-col" data-testid="admin-layout">
      <nav
        aria-label={t('nav.dashboard')}
        className="flex items-center gap-1 overflow-x-auto border-b border-border px-3"
      >
        {NAV.map((item) => (
          <Link
            key={item.key}
            {...item.link}
            activeOptions={{ exact: item.exact ?? false }}
            activeProps={{ 'aria-current': 'page', className: 'border-accent text-text' }}
            inactiveProps={{ className: 'border-transparent text-text-muted hover:text-text' }}
            className={cn('-mb-px border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap')}
            data-testid={`admin-nav-${item.key}`}
          >
            {t(`nav.${item.key}`)}
          </Link>
        ))}
      </nav>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <Outlet />
      </div>
    </div>
  )
}
