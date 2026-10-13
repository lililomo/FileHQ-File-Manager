import { useTranslation } from 'react-i18next'
import { Progress } from '@/components/ui/progress'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/stores/session'

export function QuotaIndicator({ collapsed = false }: { collapsed?: boolean }) {
  const { t, i18n } = useTranslation('nav')
  const user = useSessionStore((s) => s.user)
  if (!user) return null
  const lang = i18n.resolvedLanguage ?? 'id'
  const q = user.quota
  const limit = q?.limit ?? user.quotaBytes ?? 0
  const used = (q?.used ?? 0) + (q?.trash ?? 0) + (q?.versions ?? 0)
  const percent = limit > 0 ? Math.min(100, Math.round((used / limit) * 100)) : 0
  const label =
    limit > 0
      ? t('quota.usage', { used: formatBytes(used, lang), limit: formatBytes(limit, lang) })
      : t('quota.unlimited')
  const danger = percent >= 90
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
          >
            <div
              className={cn('h-full rounded-full', danger ? 'bg-danger' : 'bg-accent')}
              style={{ width: `${percent}%` }}
            />
          </div>
        </TooltipTrigger>
        <TooltipContent side="right">{label}</TooltipContent>
      </Tooltip>
    )
  }
  return (
    <div className="space-y-1 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium text-text">{t('quota.label')}</span>
        <span className={cn('tabular', danger ? 'text-danger' : 'text-text-muted')}>
          {t('quota.percent', { percent })}
        </span>
      </div>
      <Progress
        value={percent}
        aria-label={`${t('quota.label')}: ${label}`}
        className={cn('h-1.5', danger && '[&>[data-slot=progress-indicator]]:bg-danger')}
      />
      <p className="tabular text-text-muted">{label}</p>
    </div>
  )
}
