import { useTranslation } from 'react-i18next'
import { formatBytes, formatNumber } from '@/lib/format'
import { useSessionStore } from '@/stores/session'

export interface StatusBarProps {
  total: number
  selectedCount?: number
  selectedBytes?: number
}

export function StatusBar({ total, selectedCount = 0, selectedBytes = 0 }: StatusBarProps) {
  const { t, i18n } = useTranslation('files')
  const lang = i18n.resolvedLanguage ?? 'id'
  const user = useSessionStore((s) => s.user)
  const q = user?.quota
  const limit = q?.limit ?? user?.quotaBytes ?? 0
  const used = (q?.used ?? 0) + (q?.trash ?? 0) + (q?.versions ?? 0)
  return (
    <div
      className="flex h-7 shrink-0 items-center gap-3 border-t border-border bg-surface px-3 text-xs text-text-muted tabular"
      role="status"
      aria-live="off"
      data-testid="status-bar"
    >
      <span data-testid="status-items">
        {t('status.items', { count: total, n: formatNumber(total, lang) })}
      </span>
      {selectedCount > 0 ? (
        <span data-testid="status-selected">
          {t('status.selected', {
            count: selectedCount,
            n: formatNumber(selectedCount, lang),
            size: formatBytes(selectedBytes, lang),
          })}
        </span>
      ) : null}
      <span className="ml-auto">
        {limit > 0
          ? t('status.quota', { used: formatBytes(used, lang), limit: formatBytes(limit, lang) })
          : t('status.quotaUnknown')}
      </span>
    </div>
  )
}
