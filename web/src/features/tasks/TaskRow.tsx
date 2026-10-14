import { ChevronDown, ChevronRight, RotateCw, Trash2, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { IconButton } from '@/components/ui/icon-button'
import { Progress } from '@/components/ui/progress'
import { cn } from '@/lib/utils'
import { getTaskLogs, isActive, type Task, type TaskLogEntry } from './api'
import { taskLabel, taskMeta } from './taskLabel'

type LooseT = (key: string, options?: Record<string, unknown>) => string

export interface TaskRowProps {
  task: Task
  onCancel: (id: string) => void
  onRetry: (id: string) => void
  onDelete: (id: string) => void
}

const STATE_VARIANT: Record<Task['state'], 'default' | 'secondary' | 'destructive' | 'outline'> = {
  queued: 'outline',
  running: 'default',
  done: 'secondary',
  failed: 'destructive',
  cancelled: 'outline',
  interrupted: 'destructive',
}

export function TaskRow({ task, onCancel, onRetry, onDelete }: TaskRowProps) {
  const { t, i18n } = useTranslation(['tasks', 'common'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const [open, setOpen] = useState(false)
  const [logs, setLogs] = useState<TaskLogEntry[] | null>(null)
  const label = taskLabel(task, lt, lang)
  const meta = taskMeta(task, lt, lang)
  const active = isActive(task)
  const indeterminate = task.state === 'running' && task.progress.total <= 0
  const toggleLogs = async () => {
    const next = !open
    setOpen(next)
    if (next && logs === null) {
      try {
        const res = await getTaskLogs(task.id)
        setLogs(res.items)
      } catch {
        setLogs([])
      }
    }
  }
  return (
    <li
      className="border-b border-border px-3 py-2 last:border-b-0"
      data-testid="task-row"
      data-task-id={task.id}
      data-state={task.state}
    >
      <div className="flex items-start gap-2">
        <IconButton
          label={open ? lt('tasks:panel.hideLog') : lt('tasks:panel.showLog')}
          onClick={() => void toggleLogs()}
          aria-expanded={open}
          className="mt-0.5 size-6"
        >
          {open ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}
        </IconButton>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-medium text-text" title={label}>
              {label}
            </span>
            <Badge
              variant={STATE_VARIANT[task.state]}
              className="shrink-0"
              data-testid="task-state"
            >
              {lt(`tasks:state.${task.state}`)}
            </Badge>
          </div>
          {active ? (
            <Progress
              value={indeterminate ? 0 : task.progress.percent}
              className={cn('mt-1.5 h-1.5', indeterminate && 'animate-pulse')}
              aria-label={lt('tasks:panel.progress', {
                percent: Math.round(task.progress.percent),
              })}
              data-testid="task-progress"
            />
          ) : null}
          {meta ? (
            <p className="mt-1 truncate text-xs text-text-muted" data-testid="task-meta">
              {meta}
            </p>
          ) : null}
          {task.cancelRequested && task.state === 'running' ? (
            <p className="mt-1 text-xs text-text-muted">{lt('tasks:panel.cancelling')}</p>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          {task.cancelable ? (
            <IconButton
              label={lt('tasks:actions.cancel')}
              onClick={() => onCancel(task.id)}
              disabled={task.cancelRequested}
              data-testid="task-cancel"
              className="size-7"
            >
              <X aria-hidden="true" />
            </IconButton>
          ) : null}
          {task.retryable ? (
            <IconButton
              label={lt('tasks:actions.retry')}
              onClick={() => onRetry(task.id)}
              data-testid="task-retry"
              className="size-7"
            >
              <RotateCw aria-hidden="true" />
            </IconButton>
          ) : null}
          {!active ? (
            <IconButton
              label={lt('tasks:actions.remove')}
              onClick={() => onDelete(task.id)}
              data-testid="task-remove"
              className="size-7"
            >
              <Trash2 aria-hidden="true" />
            </IconButton>
          ) : null}
        </div>
      </div>
      {open ? (
        <div className="mt-2 max-h-40 overflow-auto rounded-md bg-surface-2 p-2 font-mono text-[11px] text-text-muted">
          {logs === null ? (
            <span>{lt('common:status.loading')}</span>
          ) : logs.length === 0 ? (
            <span>{lt('tasks:panel.noLog')}</span>
          ) : (
            logs.map((l) => (
              <div key={l.id} className={cn(l.level === 'error' && 'text-danger')}>
                <span className="opacity-70">{l.at.slice(11, 19)}</span> {l.message}
              </div>
            ))
          )}
        </div>
      ) : null}
    </li>
  )
}
