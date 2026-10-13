import { Hammer } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/EmptyState'
import type { resources } from '@/i18n'

export type NavKey = Exclude<keyof (typeof resources)['id']['nav'] & string, 'quota'>

/** Stand-in for screens built in later steps; shows which step delivers it. */
export function PlaceholderPage({ step, titleKey }: { step: string; titleKey: NavKey }) {
  const { t } = useTranslation(['nav', 'common'])
  return (
    <div className="p-6">
      <h1 className="text-xl font-semibold">{t(`nav:${titleKey}`)}</h1>
      <EmptyState
        className="mt-6 rounded-lg border border-dashed"
        icon={Hammer}
        title={t('common:placeholder.title', { step })}
        description={t('common:placeholder.body')}
      />
    </div>
  )
}
