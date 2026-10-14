import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Progress } from '@/components/ui/progress'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  QUOTA_DANGER_PERCENT,
  QUOTA_NEAR_FULL_PERCENT,
  QUOTA_WARN_PERCENT,
  quotaPercent,
} from '@/features/quota/api'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/stores/session'

/** Sidebar quota bar (SPEC §8.2): warning colour ≥ 80 %, danger ≥ 95 %,
 *  explicit "almost full" line ≥ 90 %; tooltip breaks the usage down into
 *  files, Recycle Bin and versions. */
export function QuotaIndicator({ collapsed = false }: { collapsed?: boolean }) {
  const { t, i18n } = useTranslation('nav')
  const user = useSessionStore((s) => s.user)
  if (!user) return null
  const lang = i18n.resolvedLanguage ?? 'id'
  const q = user.quota
  const limit = q?.limit ?? user.quotaBytes ?? 0
  const used = q?.used ?? 0
  const trash = q?.trash ?? 0
  const versions = q?.versions ?? 0
  const total = used + trash + versions
  const percent = quotaPercent(limit, total)
  const label =
    limit > 0
      ? t('quota.usage', { used: formatBytes(total, lang), limit: formatBytes(limit, lang) })
      : t('quota.unlimited')
  const level =
    limit > 0 && percent >= QUOTA_DANGER_PERCENT
      ? 'danger'
      : limit > 0 && percent >= QUOTA_WARN_PERCENT
        ? 'warning'
        : 'ok'
  const nearFull = limit > 0 && percent >= QUOTA_NEAR_FULL_PERCENT
  const barClass =
    level === 'danger' ? 'bg-danger' : level === 'warning' ? 'bg-warning' : 'bg-accent'
  const breakdown = (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs tabular">
      <dt>{t('quota.used')}</dt>
      <dd>{formatBytes(used, lang)}</dd>
      <dt>{t('quota.trash')}</dt>
      <dd>{formatBytes(trash, lang)}</dd>
      <dt>{t('quota.versions')}</dt>
      <dd>{formatBytes(versions, lang)}</dd>
      {limit > 0 ? (
        <>
          <dt>{t('quota.free')}</dt>
          <dd>{formatBytes(Math.max(0, limit - total), lang)}</dd>
        </>
      ) : null}
    </dl>
  )
  if (collapsed) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            role="progressbar"
            aria-label={`${t('quota.label')}: ${label}`}
            aria-valuenow={percent}
            aria-valuemin={0}
            aria-valuemax={100}
            className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2"
            data-testid="quota-indicator"
            data-percent={percent}
            data-level={level}
          >
            <div className={cn('h-full rounded-full', barClass)} style={{ width: `${percent}%` }} />
          </div>
        </TooltipTrigger>
        <TooltipContent side="right">
          <p className="mb-1">{label}</p>
          {breakdown}
        </TooltipContent>
      </Tooltip>
    )
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div
          className="space-y-1 text-xs"
          data-testid="quota-indicator"
          data-percent={percent}
          data-level={level}
        >
          <div className="flex items-center justify-between">
            <span className="font-medium text-text">{t('quota.label')}</span>
            <span
              className={cn(
                'tabular',
                level === 'danger'
                  ? 'text-danger'
                  : level === 'warning'
                    ? 'text-warning'
                    : 'text-text-muted',
              )}
            >
              {t('quota.percent', { percent })}
            </span>
          </div>
          <Progress
            value={percent}
            aria-label={`${t('quota.label')}: ${label}`}
            className={cn('h-1.5', `[&>[data-slot=progress-indicator]]:${barClass}`)}
          />
          <p className="tabular text-text-muted" data-testid="quota-usage">
            {label}
          </p>
          {nearFull ? (
            <p
              className={cn(
                'flex items-center gap-1',
                level === 'danger' ? 'text-danger' : 'text-warning',
              )}
              role="alert"
              data-testid="quota-warning"
            >
              <AlertTriangle className="size-3.5 shrink-0" aria-hidden="true" />
              {percent >= 100 ? t('quota.full') : t('quota.nearFull')}
            </p>
          ) : null}
        </div>
      </TooltipTrigger>
      <TooltipContent side="right">{breakdown}</TooltipContent>
    </Tooltip>
  )
}
