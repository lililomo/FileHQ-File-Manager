import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, Check, RefreshCw, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
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
import { cn } from '@/lib/utils'
import {
  adminKeys,
  getDashboard,
  type Dashboard as DashboardData,
  type DashboardWarning,
} from './api'

/** apt package per tool for the install hint (Ubuntu 24.04). */
const APT: Record<string, string> = {
  ffmpeg: 'ffmpeg',
  ffprobe: 'ffmpeg',
  vipsthumbnail: 'libvips-tools',
  pdftoppm: 'poppler-utils',
  '7z': '7zip',
  rg: 'ripgrep',
  unrar: 'unrar',
  rar: 'rar',
}

function Stat({
  label,
  value,
  sub,
  testId,
}: {
  label: string
  value: string | number
  sub?: string
  testId?: string
}) {
  return (
    <div className="rounded-md border border-border bg-surface px-3 py-2" data-testid={testId}>
      <div className="text-xs text-text-muted">{label}</div>
      <div className="text-lg font-semibold tabular">{value}</div>
      {sub ? <div className="text-xs text-text-muted">{sub}</div> : null}
    </div>
  )
}

/** `/admin` (SPEC §8.17 "Dashboard"): dense cards, stacked storage bar,
 *  top users, dependency table with install hints, configuration warnings. */
export function Dashboard() {
  const { t, i18n } = useTranslation('admin')
  const lang = i18n.resolvedLanguage ?? 'id'
  const query = useQuery({
    queryKey: adminKeys.dashboard,
    queryFn: ({ signal }) => getDashboard(signal),
    staleTime: 15_000,
    refetchInterval: 60_000,
  })
  const d: DashboardData | undefined = query.data
  const warningText = (w: DashboardWarning): string => {
    const det = w.details ?? {}
    const fmt = (v: unknown) => (typeof v === 'number' ? formatBytes(v, lang) : String(v ?? ''))
    return t(`dashboard.warn.${w.code}` as 'dashboard.warn.swap_low', {
      swap: fmt(det.swapTotal),
      free: fmt(det.free),
      total: fmt(det.total),
      quotaSum: fmt(det.quotaSum),
      diskTotal: fmt(det.diskTotal),
      tool: String(det.tool ?? ''),
      path: String(det.path ?? ''),
      feature: String(det.feature ?? ''),
      defaultValue: w.code,
    })
  }
  return (
    <div className="space-y-4 p-4" data-testid="admin-dashboard">
      <header className="flex items-center gap-2">
        <h1 className="text-lg font-semibold">{t('dashboard.title')}</h1>
        <span className="flex-1" />
        <Button
          variant="outline"
          size="sm"
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          <RefreshCw
            className={cn('size-4', query.isFetching && 'animate-spin')}
            aria-hidden="true"
          />
          {t('dashboard.refresh')}
        </Button>
      </header>
      {query.isPending ? (
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4" aria-busy="true">
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
      ) : query.isError ? (
        <EmptyState icon={AlertTriangle} title={errorMessage(query.error)} />
      ) : d ? (
        <>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            <Stat
              label={t('dashboard.usersActive')}
              value={d.users.active}
              testId="stat-users-active"
            />
            <Stat label={t('dashboard.usersDisabled')} value={d.users.disabled} />
            <Stat
              label={t('dashboard.storageUsed')}
              value={formatBytes(d.storage.usedBytes, lang)}
              sub={`${t('dashboard.quotaSum')}: ${formatBytes(d.storage.quotaSumBytes, lang)}`}
            />
            <Stat
              label={t('dashboard.disk', { path: '' }).trim()}
              value={formatBytes(d.storage.disk.total, lang)}
              sub={t('dashboard.diskFree', {
                free: formatBytes(d.storage.disk.free, lang),
                total: formatBytes(d.storage.disk.total, lang),
              })}
              testId="stat-disk"
            />
            <Stat
              label={t('dashboard.tasksRunning')}
              value={d.tasks.running}
              sub={t('dashboard.tasksQueued', { n: d.tasks.queued })}
            />
            <Stat label={t('dashboard.publicShares')} value={d.publicShares} />
            <Stat label={t('dashboard.failedLogins')} value={d.failedLogins24h} />
            <Stat
              label={t('dashboard.version')}
              value={d.version.version}
              sub={t('dashboard.versionLine', {
                version: d.version.version,
                commit: d.version.commit,
                go: d.version.go,
              })}
              testId="stat-version"
            />
          </div>
          <StorageBar d={d} lang={lang} />
          <section className="grid gap-4 lg:grid-cols-2">
            <div>
              <h2 className="mb-1 text-sm font-semibold">{t('dashboard.topUsers')}</h2>
              {d.topUsers.length === 0 ? (
                <p className="text-sm text-text-muted">{t('dashboard.topEmpty')}</p>
              ) : (
                <Table data-testid="top-users">
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('dashboard.colUser')}</TableHead>
                      <TableHead className="text-right">{t('dashboard.colUsed')}</TableHead>
                      <TableHead className="text-right">{t('dashboard.colQuota')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {d.topUsers.map((u) => (
                      <TableRow key={u.id}>
                        <TableCell>
                          <span className="font-medium">{u.username}</span>{' '}
                          <span className="text-text-muted">{u.displayName}</span>
                        </TableCell>
                        <TableCell className="text-right tabular">
                          {formatBytes(u.used, lang)}
                        </TableCell>
                        <TableCell className="text-right tabular">
                          {formatBytes(u.quota, lang)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </div>
            <div>
              <h2 className="mb-1 text-sm font-semibold">{t('dashboard.warnings')}</h2>
              {d.warnings.length === 0 ? (
                <p className="text-sm text-text-muted">{t('dashboard.noWarnings')}</p>
              ) : (
                <ul className="space-y-1 text-sm" data-testid="dashboard-warnings">
                  {d.warnings.map((w, i) => (
                    <li
                      key={`${w.code}-${i}`}
                      className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning/10 px-2 py-1"
                      data-code={w.code}
                    >
                      <AlertTriangle
                        className="mt-0.5 size-4 shrink-0 text-warning"
                        aria-hidden="true"
                      />
                      <span>{warningText(w)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </section>
          <section>
            <h2 className="mb-1 text-sm font-semibold">{t('dashboard.deps')}</h2>
            <Table data-testid="deps-table">
              <TableHeader>
                <TableRow>
                  <TableHead>{t('dashboard.depTool')}</TableHead>
                  <TableHead>{t('dashboard.depStatus')}</TableHead>
                  <TableHead>{t('dashboard.depPath')}</TableHead>
                  <TableHead>{t('dashboard.depVersion')}</TableHead>
                  <TableHead>{t('dashboard.depFeature')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.deps.map((dep) => {
                  const disabled = !dep.path && dep.optional
                  return (
                    <TableRow key={dep.name} data-tool={dep.name} data-ok={dep.ok}>
                      <TableCell className="font-mono text-xs">{dep.name}</TableCell>
                      <TableCell>
                        {dep.ok ? (
                          <span className="inline-flex items-center gap-1 text-success">
                            <Check className="size-4" aria-hidden="true" /> {t('dashboard.depOk')}
                          </span>
                        ) : disabled ? (
                          <span className="text-text-muted">{t('dashboard.depDisabled')}</span>
                        ) : (
                          <span className="inline-flex items-center gap-1 text-danger">
                            <X className="size-4" aria-hidden="true" /> {t('dashboard.depMissing')}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{dep.path || '—'}</TableCell>
                      <TableCell className="max-w-[16rem] truncate text-xs">
                        {dep.version || '—'}
                      </TableCell>
                      <TableCell className="text-xs">
                        {dep.feature}
                        {!dep.ok && !disabled && APT[dep.name] ? (
                          <div className="font-mono text-text-muted">
                            {t('dashboard.depHint', { pkg: APT[dep.name] })}
                          </div>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
            <p className="mt-2 text-xs text-text-muted">
              {t('dashboard.builtAt', { date: formatDate(d.version.buildDate, lang) })}
            </p>
          </section>
        </>
      ) : null}
    </div>
  )
}

function StorageBar({ d, lang }: { d: DashboardData; lang: string }) {
  const { t } = useTranslation('admin')
  const total = Math.max(d.storage.disk.total, 1)
  const used = Math.min(d.storage.usedBytes, total)
  const quota = Math.min(d.storage.quotaSumBytes, total)
  const otherUsed = Math.max(0, Math.min(d.storage.disk.used, total) - used)
  const pct = (n: number) => `${((n / total) * 100).toFixed(1)}%`
  return (
    <div data-testid="storage-bar">
      <div
        className="flex h-3 w-full overflow-hidden rounded bg-surface-2"
        role="img"
        aria-label={`${t('dashboard.storageUsed')} ${formatBytes(d.storage.usedBytes, lang)}, ${t('dashboard.quotaSum')} ${formatBytes(d.storage.quotaSumBytes, lang)}, ${t('dashboard.diskFree', { free: formatBytes(d.storage.disk.free, lang), total: formatBytes(d.storage.disk.total, lang) })}`}
      >
        <div className="bg-accent" style={{ width: pct(used) }} />
        <div className="bg-text-muted/40" style={{ width: pct(otherUsed) }} />
        <div className="bg-accent/25" style={{ width: pct(Math.max(0, quota - used)) }} />
      </div>
      <div className="mt-1 flex flex-wrap gap-x-4 text-xs text-text-muted">
        <span>
          <span className="inline-block size-2 rounded-sm bg-accent" /> {t('dashboard.storageUsed')}{' '}
          {formatBytes(d.storage.usedBytes, lang)}
        </span>
        <span>
          <span className="inline-block size-2 rounded-sm bg-accent/25" /> {t('dashboard.quotaSum')}{' '}
          {formatBytes(d.storage.quotaSumBytes, lang)}
        </span>
        <span>
          <span className="inline-block size-2 rounded-sm bg-text-muted/40" />{' '}
          {t('dashboard.diskFree', {
            free: formatBytes(d.storage.disk.free, lang),
            total: formatBytes(d.storage.disk.total, lang),
          })}
        </span>
      </div>
    </div>
  )
}
