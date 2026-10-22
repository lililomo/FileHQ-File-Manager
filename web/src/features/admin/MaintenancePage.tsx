import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, Play, Wrench } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { errorMessage } from '@/lib/errors'
import { formatBytes, formatDate } from '@/lib/format'
import {
  backupUrl,
  getMaintenance,
  opsKeys,
  runMaintenance,
  type IntegrityReport,
  type MaintenanceJob,
  type RunInput,
} from './opsApi'

type LooseT = (key: string, options?: Record<string, unknown>) => string

interface RunRequest {
  job: MaintenanceJob
  variant: 'run' | 'prune' | 'force'
}

/** `/admin/maintenance` (SPEC §8.17 "Maintenance", §7.3): one card per job
 *  with its schedule (TZ_DEFAULT), next/last run, a run button (confirmed;
 *  "purge everything" needs the word HAPUS) and the last JSON result —
 *  the integrity check as tables; then the backups and the other scheduled
 *  jobs. Every run is a system task; the page polls while one is active. */
export function MaintenancePage() {
  const { t, i18n } = useTranslation(['admin', 'tasks', 'common'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const [req, setReq] = useState<RunRequest | null>(null)
  const query = useQuery({
    queryKey: opsKeys.maintenance,
    queryFn: ({ signal }) => getMaintenance(signal),
    refetchInterval: (q) =>
      q.state.data?.jobs.some(
        (j) => j.lastTask?.state === 'queued' || j.lastTask?.state === 'running',
      )
        ? 1500
        : 15000,
  })
  const run = useMutation({
    mutationFn: ({ job, body }: { job: MaintenanceJob; body: RunInput }) =>
      runMaintenance(job.slug ?? job.name, body),
    onSuccess: (_, { job }) => {
      setReq(null)
      toast.success(t('admin:maint.started', { job: lt(`tasks:label.job.${job.name}`) }))
      void qc.invalidateQueries({ queryKey: opsKeys.maintenance })
      void qc.invalidateQueries({ queryKey: opsKeys.tasks })
    },
    onError: (e) => {
      setReq(null)
      toast.error(errorMessage(e))
    },
  })
  if (query.isPending)
    return (
      <div className="grid gap-3 p-4 md:grid-cols-2" aria-busy="true">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-40" />
        ))}
      </div>
    )
  if (query.isError) return <EmptyState icon={Wrench} title={errorMessage(query.error)} />
  const data = query.data
  const manual = data.jobs.filter((j) => j.manual)
  const other = data.jobs.filter((j) => !j.manual)
  const when = (iso?: string) => (iso ? formatDate(iso, lang, { time: true }) : '—')

  return (
    <div className="space-y-6 p-4" data-testid="admin-maintenance">
      <header className="flex flex-wrap items-baseline gap-2">
        <h1 className="text-lg font-semibold">{t('admin:maint.title')}</h1>
        <span className="text-sm text-text-muted">
          {t('admin:maint.timeZone', { tz: data.timeZone })}
        </span>
      </header>
      <div className="grid gap-3 lg:grid-cols-2">
        {manual.map((job) => {
          const task = job.lastTask
          const active = task?.state === 'queued' || task?.state === 'running'
          return (
            <section
              key={job.name}
              className="flex flex-col gap-2 rounded-lg border border-border bg-surface p-4"
              data-testid={`maint-card-${job.name}`}
              aria-labelledby={`maint-${job.name}`}
            >
              <div className="flex items-start gap-2">
                <div className="min-w-0 flex-1">
                  <h2 id={`maint-${job.name}`} className="font-medium">
                    {lt(`tasks:label.job.${job.name}`)}
                  </h2>
                  <p className="text-xs text-text-muted">{lt(`admin:maint.desc.${job.name}`)}</p>
                </div>
                {task ? (
                  <Badge
                    variant={
                      task.state === 'failed' || task.state === 'interrupted'
                        ? 'destructive'
                        : task.state === 'done'
                          ? 'secondary'
                          : 'default'
                    }
                    data-testid="maint-state"
                  >
                    {lt(`tasks:state.${task.state}`)}
                  </Badge>
                ) : null}
              </div>
              <dl className="grid grid-cols-[7rem_1fr] gap-x-2 text-xs">
                <dt className="text-text-muted">{t('admin:maint.schedule')}</dt>
                <dd className="font-mono">{job.schedule?.schedule ?? '—'}</dd>
                <dt className="text-text-muted">{t('admin:maint.nextRun')}</dt>
                <dd>{when(job.schedule?.nextRun)}</dd>
                <dt className="text-text-muted">{t('admin:maint.lastRun')}</dt>
                <dd>{when(task?.finishedAt ?? task?.startedAt ?? task?.createdAt)}</dd>
              </dl>
              {active && task ? (
                <Progress
                  value={task.progress.total > 0 ? task.progress.percent : 0}
                  className="h-1.5"
                  aria-label={t('tasks:panel.progress', {
                    percent: Math.round(task.progress.percent),
                  })}
                />
              ) : null}
              {task?.error ? (
                <p className="text-xs text-danger">
                  {task.error.code}
                  {task.error.message ? `: ${task.error.message}` : ''}
                </p>
              ) : null}
              {task?.state === 'done' && task.result ? (
                <JobResult name={job.name} result={task.result} />
              ) : null}
              <div className="mt-auto flex flex-wrap gap-2 pt-1">
                <Button
                  size="sm"
                  disabled={active || run.isPending}
                  onClick={() => setReq({ job, variant: 'run' })}
                  data-testid={`maint-run-${job.name}`}
                >
                  <Play className="size-4" aria-hidden="true" />
                  {t(job.name === 'clear_thumbs' ? 'admin:maint.clearAll' : 'admin:maint.run')}
                </Button>
                {job.name === 'clear_thumbs' ? (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={active || run.isPending}
                    onClick={() => setReq({ job, variant: 'prune' })}
                  >
                    {t('admin:maint.prune')}
                  </Button>
                ) : null}
                {job.name === 'purge_trash' ? (
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={active || run.isPending}
                    onClick={() => setReq({ job, variant: 'force' })}
                    data-testid="maint-force-purge"
                  >
                    {t('admin:maint.forceAll')}
                  </Button>
                ) : null}
              </div>
            </section>
          )
        })}
      </div>

      <section aria-labelledby="maint-backups">
        <h2 id="maint-backups" className="mb-2 font-medium">
          {t('admin:maint.backups')}
        </h2>
        {data.backups.length === 0 ? (
          <p className="text-sm text-text-muted">{t('admin:maint.noBackups')}</p>
        ) : (
          <Table className="text-xs" data-testid="backup-table">
            <TableHeader>
              <TableRow>
                <TableHead>{t('admin:maint.backupName')}</TableHead>
                <TableHead>{t('admin:maint.backupSize')}</TableHead>
                <TableHead>{t('admin:maint.backupCreated')}</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.backups.map((b) => (
                <TableRow key={b.name} data-testid="backup-row">
                  <TableCell className="font-mono">
                    {b.name}
                    {b.label ? (
                      <Badge variant="outline" className="ml-2">
                        {t('admin:maint.labelled')}
                      </Badge>
                    ) : null}
                  </TableCell>
                  <TableCell className="tabular">{formatBytes(b.size, lang)}</TableCell>
                  <TableCell>{when(b.createdAt)}</TableCell>
                  <TableCell>
                    <Button asChild variant="ghost" size="icon-sm">
                      <a
                        href={backupUrl(b.name)}
                        download
                        aria-label={t('admin:maint.download', { name: b.name })}
                      >
                        <Download className="size-4" aria-hidden="true" />
                      </a>
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </section>

      {other.length > 0 ? (
        <section aria-labelledby="maint-other">
          <h2 id="maint-other" className="mb-2 font-medium">
            {t('admin:maint.otherJobs')}
          </h2>
          <Table className="text-xs">
            <TableHeader>
              <TableRow>
                <TableHead>{t('admin:maint.job')}</TableHead>
                <TableHead>{t('admin:maint.schedule')}</TableHead>
                <TableHead>{t('admin:maint.nextRun')}</TableHead>
                <TableHead>{t('admin:maint.lastRun')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {other.map((j) => (
                <TableRow key={j.name}>
                  <TableCell className="font-mono">{j.name}</TableCell>
                  <TableCell className="font-mono">{j.schedule?.schedule}</TableCell>
                  <TableCell>{when(j.schedule?.nextRun)}</TableCell>
                  <TableCell>
                    {when(j.schedule?.lastRun)}
                    {j.schedule?.lastError ? (
                      <span className="ml-2 text-danger">{j.schedule.lastError}</span>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      ) : null}

      {req ? (
        <RunDialog
          req={req}
          busy={run.isPending}
          onCancel={() => setReq(null)}
          onRun={(body) => run.mutate({ job: req.job, body })}
        />
      ) : null}
    </div>
  )
}

function RunDialog({
  req,
  busy,
  onCancel,
  onRun,
}: {
  req: RunRequest
  busy: boolean
  onCancel: () => void
  onRun: (body: RunInput) => void
}) {
  const { t } = useTranslation(['admin', 'tasks', 'common'])
  const lt = t as unknown as LooseT
  const [word, setWord] = useState('')
  const [label, setLabel] = useState('')
  const name = req.job.name
  const force = req.variant === 'force'
  const valid =
    (!force || word === 'HAPUS') && (label === '' || /^[a-z0-9][a-z0-9-]{0,31}$/.test(label))
  const submit = () => {
    if (!valid) return
    const body: RunInput = {}
    if (force) Object.assign(body, { force: true, confirm: word })
    if (req.variant === 'prune') body.prune = true
    if (name === 'backup_db' && label) body.label = label
    onRun(body)
  }
  return (
    <AlertDialog open onOpenChange={(o) => !o && onCancel()}>
      <AlertDialogContent data-testid="maint-run-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle>{lt(`tasks:label.job.${name}`)}</AlertDialogTitle>
          <AlertDialogDescription>
            {lt(
              force
                ? 'admin:maint.confirmForce'
                : req.variant === 'prune'
                  ? 'admin:maint.confirmPrune'
                  : `admin:maint.confirm.${name}`,
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {force ? (
          <div className="grid gap-1">
            <Label htmlFor="maint-word">{t('admin:maint.typeWord')}</Label>
            <Input
              id="maint-word"
              value={word}
              onChange={(e) => setWord(e.target.value)}
              autoComplete="off"
              data-testid="maint-word"
            />
          </div>
        ) : null}
        {name === 'backup_db' ? (
          <div className="grid gap-1">
            <Label htmlFor="maint-label">{t('admin:maint.label')}</Label>
            <Input
              id="maint-label"
              value={label}
              onChange={(e) => setLabel(e.target.value.toLowerCase())}
              placeholder="sebelum-update"
              aria-invalid={!valid}
            />
          </div>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel>{t('common:actions.cancel')}</AlertDialogCancel>
          <Button
            variant={force ? 'destructive' : 'default'}
            disabled={!valid || busy}
            onClick={submit}
            data-testid="maint-run-confirm"
          >
            {t('admin:maint.run')}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** Last result: integrity report as tables, other jobs as compact JSON. */
function JobResult({ name, result }: { name: string; result: Record<string, unknown> }) {
  const { t } = useTranslation('admin')
  const lt = t as unknown as LooseT
  if (name === 'integrity_check') {
    const r = result as unknown as IntegrityReport
    const sections: { key: string; rows: string[][] }[] = [
      { key: 'orphanFolders', rows: (r.orphanFolders ?? []).map((o) => [o.name]) },
      {
        key: 'missingHomes',
        rows: (r.missingHomes ?? []).map((m) => [
          m.username,
          m.provisioned ? t('maint.integrity.missing') : t('maint.integrity.notYet'),
        ]),
      },
      { key: 'brokenRefs', rows: (r.brokenRefs ?? []).map((b) => [b.table, b.username, b.path]) },
      { key: 'orphanSystemDirs', rows: (r.orphanSystemDirs ?? []).map((o) => [o.area, o.name]) },
    ]
    return (
      <div className="space-y-2 text-xs" data-testid="integrity-report">
        {sections.map((s) => (
          <div key={s.key} data-testid={`integrity-${s.key}`}>
            <div className="font-medium">
              {lt(`maint.integrity.${s.key}`)} ({s.rows.length})
            </div>
            {s.rows.length === 0 ? (
              <div className="text-text-muted">{t('maint.integrity.none')}</div>
            ) : (
              <ul className="max-h-32 overflow-auto font-mono">
                {s.rows.map((row) => (
                  <li key={row.join('|')}>{row.join(' · ')}</li>
                ))}
              </ul>
            )}
          </div>
        ))}
        {r.truncated ? <div className="text-warning">{t('maint.integrity.truncated')}</div> : null}
      </div>
    )
  }
  return (
    <pre
      className="max-h-32 overflow-auto rounded-md bg-surface-2 p-2 font-mono text-[11px]"
      data-testid="maint-result"
    >
      {JSON.stringify(result, null, 1)}
    </pre>
  )
}
