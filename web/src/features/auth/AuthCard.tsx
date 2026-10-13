import type { ReactNode } from 'react'
import { AppLogo } from '@/components/AppLogo'
import { LanguageToggle } from '@/components/shell/LanguageToggle'
import { ThemeToggle } from '@/components/shell/ThemeToggle'
import { useAppName } from './useAppName'

/** 360 px card centred on the surface colour (SPEC §9.3 Login). */
export function AuthCard({
  title,
  subtitle,
  children,
}: {
  title: string
  subtitle?: string
  children: ReactNode
}) {
  const appName = useAppName()
  return (
    <div className="flex min-h-dvh flex-col bg-surface text-text">
      <div className="flex justify-end gap-0.5 p-2">
        <LanguageToggle />
        <ThemeToggle />
      </div>
      <main className="flex flex-1 items-start justify-center px-4 pb-12 pt-4 sm:items-center">
        <div className="w-full max-w-[360px] rounded-lg border border-border bg-bg p-6 shadow-popover">
          <div className="mb-5 flex items-center gap-2 text-text-muted">
            <AppLogo />
            <span className="text-sm font-medium">{appName}</span>
          </div>
          <h1 className="text-xl font-semibold">{title}</h1>
          {subtitle ? <p className="mt-1 text-sm text-text-muted">{subtitle}</p> : null}
          <div className="mt-5">{children}</div>
        </div>
      </main>
    </div>
  )
}
