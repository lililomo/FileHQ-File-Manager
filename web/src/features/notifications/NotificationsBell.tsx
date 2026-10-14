import { useNavigate } from '@tanstack/react-router'
import { Bell, Check, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { errorMessage } from '@/features/file-ops/useFileOps'
import { formatRelativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useTasksStore } from '@/stores/tasks'
import { deleteNotification, markNotificationsRead, type Notification } from './api'

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** Bell with unread badge and a popover of the 20 latest notifications
 *  (SPEC §8.16): mark one/all read, delete, follow the link. */
export function NotificationsBell() {
  const { t, i18n } = useTranslation(['tasks', 'common'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const navigate = useNavigate()
  const items = useTasksStore((s) => s.notifications)
  const unread = useTasksStore((s) => s.unread)
  const open = useTasksStore((s) => s.bellOpen)
  const setOpen = useTasksStore((s) => s.setBell)
  const fail = (e: unknown) => toast.error(errorMessage(lt, e))
  const readOne = async (n: Notification) => {
    if (n.readAt) return
    useTasksStore.getState().markRead([n.id])
    try {
      const res = await markNotificationsRead([n.id])
      useTasksStore.getState().markRead([n.id], res.unread)
    } catch (e) {
      fail(e)
    }
  }
  const readAll = async () => {
    useTasksStore.getState().markRead('all')
    try {
      await markNotificationsRead('all')
    } catch (e) {
      fail(e)
    }
  }
  const remove = async (n: Notification) => {
    useTasksStore.getState().removeNotification(n.id)
    try {
      await deleteNotification(n.id)
    } catch (e) {
      fail(e)
    }
  }
  const follow = async (n: Notification) => {
    await readOne(n)
    if (n.link) {
      setOpen(false)
      void navigate({ to: n.link })
    }
  }
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <IconButton
          label={lt('tasks:bell.label', { count: unread })}
          className="relative"
          data-testid="notifications-bell"
        >
          <Bell aria-hidden="true" />
          {unread > 0 ? (
            <span
              className="absolute -top-0.5 -right-0.5 min-w-4 rounded-full bg-danger px-1 text-center text-[10px] font-semibold leading-4 text-white"
              data-testid="notifications-unread"
            >
              {unread > 99 ? '99+' : unread}
            </span>
          ) : null}
        </IconButton>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 p-0" data-testid="notifications-popover">
        <header className="flex items-center justify-between border-b border-border px-3 py-2">
          <h2 className="text-sm font-semibold">{lt('tasks:bell.title')}</h2>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            onClick={() => void readAll()}
            disabled={unread === 0}
            data-testid="notifications-read-all"
          >
            <Check aria-hidden="true" className="size-3.5" />
            {lt('tasks:bell.readAll')}
          </Button>
        </header>
        <ul className="max-h-80 overflow-auto">
          {items.length === 0 ? (
            <li className="px-3 py-6 text-center text-sm text-text-muted">
              {lt('tasks:bell.empty')}
            </li>
          ) : (
            items.map((n) => (
              <li
                key={n.id}
                className={cn(
                  'flex items-start gap-2 border-b border-border px-3 py-2 last:border-b-0',
                  !n.readAt && 'bg-accent-subtle/40',
                )}
                data-testid="notification-row"
                data-unread={n.readAt ? undefined : 'true'}
              >
                <button
                  type="button"
                  className="min-w-0 flex-1 text-left"
                  onClick={() => void follow(n)}
                >
                  <span className="block truncate text-sm font-medium text-text">{n.title}</span>
                  {n.body ? (
                    <span className="block truncate text-xs text-text-muted">{n.body}</span>
                  ) : null}
                  <span className="block text-[11px] text-text-muted">
                    {lt(`tasks:bell.type.${n.type}`, { defaultValue: n.type })} ·{' '}
                    {formatRelativeTime(n.createdAt, lang)}
                  </span>
                </button>
                <IconButton
                  label={lt('common:actions.close')}
                  className="size-6"
                  onClick={() => void remove(n)}
                  data-testid="notification-delete"
                >
                  <Trash2 aria-hidden="true" />
                </IconButton>
              </li>
            ))
          )}
        </ul>
      </PopoverContent>
    </Popover>
  )
}
