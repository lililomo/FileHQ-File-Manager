import { ListChecks, WifiOff, X } from 'lucide-react'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { IconButton } from '@/components/ui/icon-button'
import { announce } from '@/lib/announce'
import { errorMessage } from '@/features/file-ops/useFileOps'
import { activeCount, useTasksStore } from '@/stores/tasks'
import { cancelTask, deleteTask, retryTask } from './api'
import { TaskRow } from './TaskRow'

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** Bottom-right task panel (SPEC §7.4): stays mounted across routes, sits
 *  below the transfer panel (Step 12a) and never covers the toasts
 *  (bottom-left). */
export function TaskPanel() {
  const { t } = useTranslation(['tasks', 'common'])
  const lt = t as unknown as LooseT
  const open = useTasksStore((s) => s.panelOpen)
  const setPanel = useTasksStore((s) => s.setPanel)
  const order = useTasksStore((s) => s.order)
  const tasks = useTasksStore((s) => s.tasks)
  const connection = useTasksStore((s) => s.connection)
  const fail = useCallback((e: unknown) => toast.error(errorMessage(lt, e)), [lt])
  const onCancel = useCallback(
    async (id: string) => {
      try {
        const res = await cancelTask(id)
        useTasksStore.getState().upsert(res.task)
        announce(lt('tasks:toast.cancelRequested'))
      } catch (e) {
        fail(e)
      }
    },
    [fail, lt],
  )
  const onRetry = useCallback(
    async (id: string) => {
      try {
        const res = await retryTask(id)
        useTasksStore.getState().upsert(res.task)
        announce(lt('tasks:toast.retried'))
      } catch (e) {
        fail(e)
      }
    },
    [fail, lt],
  )
  const onDelete = useCallback(
    async (id: string) => {
      try {
        await deleteTask(id)
        useTasksStore.getState().remove(id)
      } catch (e) {
        fail(e)
      }
    },
    [fail],
  )
  if (!open) return null
  const active = activeCount(tasks)
  return (
    <section
      aria-label={lt('tasks:panel.title')}
      data-testid="task-panel"
      className="fixed right-3 bottom-3 z-40 flex max-h-[60vh] w-[min(26rem,calc(100vw-1.5rem))] flex-col rounded-lg border border-border bg-surface shadow-lg"
    >
      <header className="flex items-center gap-2 border-b border-border px-3 py-2">
        <ListChecks className="size-4 text-text-muted" aria-hidden="true" />
        <h2 className="text-sm font-semibold text-text">
          {lt('tasks:panel.title')}
          {active > 0 ? <span className="ml-1 text-text-muted">({active})</span> : null}
        </h2>
        {connection === 'reconnecting' || connection === 'closed' ? (
          <span
            className="ml-1 inline-flex items-center gap-1 text-xs text-warning"
            title={lt('tasks:panel.offline')}
            data-testid="task-offline"
          >
            <WifiOff className="size-3.5" aria-hidden="true" />
            {lt('tasks:panel.offlineShort')}
          </span>
        ) : null}
        <IconButton
          label={lt('common:actions.close')}
          onClick={() => setPanel(false)}
          className="ml-auto size-7"
          data-testid="task-panel-close"
        >
          <X aria-hidden="true" />
        </IconButton>
      </header>
      <ul className="min-h-0 flex-1 overflow-auto" aria-live="off">
        {order.length === 0 ? (
          <li className="px-3 py-6 text-center text-sm text-text-muted">
            {lt('tasks:panel.empty')}
          </li>
        ) : (
          order.map((id) => {
            const task = tasks[id]
            return task ? (
              <TaskRow
                key={id}
                task={task}
                onCancel={(x) => void onCancel(x)}
                onRetry={(x) => void onRetry(x)}
                onDelete={(x) => void onDelete(x)}
              />
            ) : null
          })
        )}
      </ul>
    </section>
  )
}
