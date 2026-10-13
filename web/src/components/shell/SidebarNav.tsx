import { Link, type LinkProps } from '@tanstack/react-router'
import {
  Clock,
  Folder,
  History,
  Link2,
  PieChart,
  Shield,
  Star,
  Trash2,
  Users,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import type { NavKey } from '@/components/PlaceholderPage'
import { useSessionStore } from '@/stores/session'
import { cn } from '@/lib/utils'

interface NavItem {
  key: NavKey
  icon: LucideIcon
  link: LinkProps
  adminOnly?: boolean
}

const ITEMS: NavItem[] = [
  { key: 'files', icon: Folder, link: { to: '/files/$', params: { _splat: '' } } as LinkProps },
  { key: 'recent', icon: Clock, link: { to: '/recent' } },
  { key: 'favorites', icon: Star, link: { to: '/favorites' } },
  { key: 'sharedWithMe', icon: Users, link: { to: '/shared-with-me' } },
  { key: 'myShares', icon: Link2, link: { to: '/shares' } },
  { key: 'trash', icon: Trash2, link: { to: '/trash' } },
  { key: 'activity', icon: History, link: { to: '/activity' } },
  { key: 'storage', icon: PieChart, link: { to: '/storage' } },
  { key: 'admin', icon: Shield, link: { to: '/admin' }, adminOnly: true },
]

export function SidebarNav({
  collapsed = false,
  onNavigate,
}: {
  collapsed?: boolean
  onNavigate?: () => void
}) {
  const { t } = useTranslation(['nav', 'common'])
  const role = useSessionStore((s) => s.user?.role)
  const items = ITEMS.filter((i) => !i.adminOnly || role === 'admin')
  return (
    <nav aria-label={t('common:a11y.mainNavigation')} className="p-2">
      <ul className="space-y-0.5">
        {items.map((item) => {
          const label = t(`nav:${item.key}`)
          const Icon = item.icon
          const link = (
            <Link
              {...item.link}
              onClick={onNavigate}
              activeOptions={{ exact: false }}
              activeProps={{ 'aria-current': 'page', className: 'bg-accent-subtle text-text' }}
              className={cn(
                'flex h-9 items-center gap-2 rounded-md px-2 text-sm text-text-muted hover:bg-surface-2 hover:text-text',
                collapsed && 'justify-center px-0',
              )}
              aria-label={collapsed ? label : undefined}
            >
              <Icon className="size-4 shrink-0" aria-hidden="true" />
              {!collapsed ? <span className="truncate">{label}</span> : null}
            </Link>
          )
          return (
            <li key={item.key}>
              {collapsed ? (
                <Tooltip>
                  <TooltipTrigger asChild>{link}</TooltipTrigger>
                  <TooltipContent side="right">{label}</TooltipContent>
                </Tooltip>
              ) : (
                link
              )}
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
