import { useMutation } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ChevronDown, LogOut, Settings } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { logout } from '@/features/auth/api'
import { useSessionStore } from '@/stores/session'

export function UserMenu() {
  const { t } = useTranslation(['common', 'auth'])
  const user = useSessionStore((s) => s.user)
  const navigate = useNavigate()
  const mutation = useMutation({
    mutationFn: logout,
    onSettled: () => {
      useSessionStore.getState().setAnonymous()
      toast.success(t('auth:logout.success'))
      void navigate({ to: '/login', search: { next: undefined } })
    },
  })
  if (!user) return null
  const name = user.displayName || user.username
  const initials = name
    .split(/\s+/)
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase() ?? '')
    .join('')
  const meta = `@${user.username} · ${t(`common:roles.${user.role}`)}`
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="h-8 gap-2 px-1.5"
          aria-label={t('common:a11y.userMenu')}
          data-testid="user-menu"
        >
          <span
            aria-hidden="true"
            className="grid size-6 place-items-center rounded-full bg-accent-subtle text-[11px] font-semibold text-text"
          >
            {initials}
          </span>
          <span className="hidden max-w-32 truncate text-sm font-medium sm:inline">{name}</span>
          <ChevronDown className="size-3.5 text-text-muted" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="font-normal">
          <div className="truncate font-medium">{name}</div>
          <div className="truncate text-xs text-text-muted">{meta}</div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/settings">
            <Settings aria-hidden="true" />
            {t('common:actions.settings')}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => mutation.mutate()}
          disabled={mutation.isPending}
          data-testid="logout"
        >
          <LogOut aria-hidden="true" />
          {t('common:actions.logout')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
