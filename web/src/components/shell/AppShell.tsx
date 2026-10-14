import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useQuotaSync } from '@/features/quota/useQuota'
import { GlobalOverlays } from './GlobalOverlays'
import { Sidebar } from './Sidebar'
import { TopBar } from './TopBar'

export function AppShell({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  useQuotaSync()
  return (
    <div className="flex h-dvh flex-col bg-bg text-text">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-50 focus:rounded-md focus:bg-accent focus:px-3 focus:py-2 focus:text-accent-fg"
      >
        {t('a11y.skipToContent')}
      </a>
      <TopBar />
      <div className="flex min-h-0 flex-1">
        <Sidebar />
        <main id="main" tabIndex={-1} className="min-w-0 flex-1 overflow-auto outline-none">
          {children}
        </main>
      </div>
      <GlobalOverlays />
    </div>
  )
}
