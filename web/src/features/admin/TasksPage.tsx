import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ListChecks } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { TaskRow } from '@/features/tasks/TaskRow'
import { errorMessage } from '@/lib/errors'
import { useDebouncedValue } from '@/lib/useDebouncedValue'
import { cancelAdminTask, listAdminTasks, opsKeys } from './opsApi'

const STATES = ['queued', 'running', 'done', 'failed', 'cancelled', 'interrupted'] as const

/** `/admin/tasks` (SPEC §8.17 "Tasks"): every user's tasks and the system
 *  (maintenance) tasks in the task panel's row component with an owner
 *  column; admins can cancel (audited). Polls while something is active. */
export function TasksPage() {
  const { t } = useTranslation(['admin', 'tasks'])
  const qc = useQueryClient()
  const [state, setState] = useState('')
  const [user, setUser] = useState('')
  const debouncedUser = useDebouncedValue(user, 300)
  const query = useInfiniteQuery({
    queryKey: [...opsKeys.tasks, state, debouncedUser],
    queryFn: ({ pageParam, signal }) =>
      listAdminTasks({ state, user: debouncedUser }, pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    refetchInterval: (q) =>
      q.state.data?.pages.some((p) =>
        p.items.some((i) => i.state === 'queued' || i.state === 'running'),
      )
        ? 2000
        : 15000,
  })
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data])
  const cancel = useMutation({
    mutationFn: cancelAdminTask,
    onSuccess: () => {
      toast.success(t('admin:tasks.cancelled'))
      void qc.invalidateQueries({ queryKey: opsKeys.tasks })
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  return (
    <div className="flex h-full flex-col" data-testid="admin-tasks">
      <header className="flex flex-wrap items-end gap-2 border-b border-border px-4 py-3">
        <h1 className="mr-2 self-center text-lg font-semibold">{t('admin:tasks.title')}</h1>
        <div className="grid gap-1">
          <Label htmlFor="tasks-state" className="text-xs">
            {t('admin:tasks.state')}
          </Label>
          <select
            id="tasks-state"
            value={state}
            onChange={(e) => setState(e.target.value)}
            className="h-8 rounded-md border border-border bg-surface px-2 text-sm"
            data-testid="tasks-state"
          >
            <option value="">{t('admin:tasks.allStates')}</option>
            {STATES.map((s) => (
              <option key={s} value={s}>
                {t(`tasks:state.${s}`)}
              </option>
            ))}
          </select>
        </div>
        <div className="grid gap-1">
          <Label htmlFor="tasks-user" className="text-xs">
            {t('admin:tasks.user')}
          </Label>
          <Input
            id="tasks-user"
            value={user}
            onChange={(e) => setUser(e.target.value)}
            placeholder={t('admin:tasks.userHint')}
            className="h-8 w-44"
            data-testid="tasks-user"
          />
        </div>
      </header>
      {query.isPending ? (
        <div className="space-y-2 p-3" aria-busy="true">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      ) : query.isError ? (
        <EmptyState icon={ListChecks} title={errorMessage(query.error)} />
      ) : rows.length === 0 ? (
        <EmptyState icon={ListChecks} title={t('admin:tasks.empty')} />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <ul className="max-w-4xl" data-testid="admin-task-list">
            {rows.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                owner={task.username ?? ''}
                onCancel={(id) => cancel.mutate(id)}
                loadLogs={async () => []}
              />
            ))}
          </ul>
          {query.hasNextPage ? (
            <div className="p-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
              >
                {t('admin:audit.more')}
              </Button>
            </div>
          ) : null}
        </div>
      )}
    </div>
  )
}
