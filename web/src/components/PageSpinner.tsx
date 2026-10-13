import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function PageSpinner() {
  const { t } = useTranslation()
  return (
    <div
      role="status"
      className="flex h-full min-h-40 items-center justify-center p-8 text-text-muted"
    >
      <Loader2 className="size-5 animate-spin" aria-hidden="true" />
      <span className="sr-only">{t('status.loading')}</span>
    </div>
  )
}
