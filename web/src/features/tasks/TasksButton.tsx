import { ListChecks } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { activeCount, useTasksStore } from '@/stores/tasks'

/** "Tasks (n)" in the top bar: toggles the task panel (SPEC §7.4). */
export function TasksButton() {
  const { t } = useTranslation(['tasks'])
  const n = useTasksStore((s) => activeCount(s.tasks))
  const open = useTasksStore((s) => s.panelOpen)
  const setPanel = useTasksStore((s) => s.setPanel)
  return (
    <Button
      variant="ghost"
      size="sm"
      className="h-8 gap-1.5 px-2"
      aria-pressed={open}
      aria-label={t('tasks:button.label', { count: n })}
      title={t('tasks:button.label', { count: n })}
      onClick={() => setPanel(!open)}
      data-testid="tasks-button"
    >
      <ListChecks aria-hidden="true" className={n > 0 ? 'animate-pulse text-accent' : ''} />
      <span className="hidden text-sm sm:inline">{t('tasks:button.text')}</span>
      {n > 0 ? (
        <span
          className="rounded-full bg-accent px-1.5 text-[11px] font-semibold text-accent-fg"
          data-testid="tasks-count"
        >
          {n}
        </span>
      ) : null}
    </Button>
  )
}
