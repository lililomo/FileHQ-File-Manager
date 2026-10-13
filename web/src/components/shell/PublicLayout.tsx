import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AppLogo } from '@/components/AppLogo'
import { useAppName } from '@/features/auth/useAppName'

/** Chrome for public share pages: thin header, centred content, one-line footer (SPEC §9.3). */
export function PublicLayout({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const appName = useAppName()
  return (
    <div className="flex min-h-dvh flex-col bg-bg text-text">
      <header className="flex h-12 items-center gap-2 border-b border-border bg-surface px-4 font-semibold">
        <AppLogo />
        <span>{appName}</span>
      </header>
      <main className="mx-auto w-full max-w-[1100px] flex-1 p-4">{children}</main>
      <footer className="border-t border-border px-4 py-3 text-center text-xs text-text-muted">
        {t('publicFooter', { app: appName })}
      </footer>
    </div>
  )
}
