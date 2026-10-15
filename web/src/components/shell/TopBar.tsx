import { Link, useNavigate } from '@tanstack/react-router'
import { PanelLeft, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { AppLogo } from '@/components/AppLogo'
import { Button } from '@/components/ui/button'
import { NotificationsBell } from '@/features/notifications/NotificationsBell'
import { TasksButton } from '@/features/tasks/TasksButton'
import { IconButton } from '@/components/ui/icon-button'
import { Kbd, KbdGroup } from '@/components/ui/kbd'
import { useSessionStore } from '@/stores/session'
import { useUIStore } from '@/stores/ui'
import { LanguageToggle } from './LanguageToggle'
import { MobileNav } from './MobileNav'
import { ThemeToggle } from './ThemeToggle'
import { UserMenu } from './UserMenu'

const SEARCH_SHORTCUT = ['Ctrl', 'K']

export function TopBar() {
  const { t } = useTranslation(['common', 'nav', 'files'])
  const navigate = useNavigate()
  const appName = useSessionStore((s) => s.config?.appName) || t('common:app.name')
  const toggleSidebar = useUIStore((s) => s.toggleSidebar)
  return (
    <header className="flex h-12 shrink-0 items-center gap-1 border-b border-border bg-surface px-2">
      <IconButton
        label={t('common:a11y.toggleSidebar')}
        onClick={toggleSidebar}
        className="hidden md:inline-flex"
      >
        <PanelLeft aria-hidden="true" />
      </IconButton>
      <MobileNav />
      <Link
        to="/files/$"
        params={{ _splat: '' }}
        className="flex items-center gap-2 rounded-md px-1.5 py-1 font-semibold text-text"
      >
        <AppLogo />
        <span>{appName}</span>
      </Link>
      <div className="mx-auto hidden w-full max-w-md px-4 sm:block">
        <Button
          variant="outline"
          className="h-8 w-full justify-start gap-2 bg-bg font-normal text-text-muted"
          onClick={() => void navigate({ to: '/search', search: {} })}
          title={t('nav:searchPlaceholder')}
          data-testid="topbar-search"
        >
          <Search aria-hidden="true" />
          <span className="truncate">{t('nav:searchPlaceholder')}</span>
          <KbdGroup className="ml-auto" title={t('files:palette.title')}>
            {SEARCH_SHORTCUT.map((k) => (
              <Kbd key={k}>{k}</Kbd>
            ))}
          </KbdGroup>
        </Button>
      </div>
      <div className="ml-auto flex items-center gap-0.5">
        <TasksButton />
        <NotificationsBell />
        <LanguageToggle />
        <ThemeToggle />
        <UserMenu />
      </div>
    </header>
  )
}
