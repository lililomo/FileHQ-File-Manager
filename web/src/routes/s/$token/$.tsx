import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { Hammer } from 'lucide-react'
import { EmptyState } from '@/components/EmptyState'
import { PublicLayout } from '@/components/shell/PublicLayout'

/** Public share page placeholder (built in Step 18a). No session required. */
export const Route = createFileRoute('/s/$token/$')({
  component: PublicSharePlaceholder,
})

function PublicSharePlaceholder() {
  const { t } = useTranslation()
  return (
    <PublicLayout>
      <h1 className="text-xl font-semibold">{t('publicShare')}</h1>
      <EmptyState
        className="mt-6 rounded-lg border border-dashed"
        icon={Hammer}
        title={t('placeholder.title', { step: '18a' })}
        description={t('placeholder.body')}
      />
    </PublicLayout>
  )
}
