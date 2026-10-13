import { Link } from '@tanstack/react-router'
import { SearchX } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/EmptyState'

export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg p-6 text-text">
      <EmptyState
        icon={SearchX}
        title={t('notFound.title')}
        description={t('notFound.body')}
        action={
          <Button asChild variant="secondary">
            <Link to="/files/$" params={{ _splat: '' }}>
              {t('notFound.action')}
            </Link>
          </Button>
        }
      />
    </div>
  )
}
