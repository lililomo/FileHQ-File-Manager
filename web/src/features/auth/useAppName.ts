import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { useSessionStore } from '@/stores/session'

interface VersionResponse {
  version: string
  appName: string
  logoUrl?: string
}

/** App name for pages rendered before login (public endpoint `/api/v1/version`). */
export function useAppName(): string {
  const { t } = useTranslation()
  const fromSession = useSessionStore((s) => s.config?.appName)
  const { data } = useQuery({
    queryKey: ['version'],
    queryFn: () => api<VersionResponse>('/version', { skipAuthRedirect: true }),
    staleTime: 5 * 60_000,
    enabled: !fromSession,
  })
  return fromSession || data?.appName || t('app.name')
}

/** URL of the admin-uploaded logo, or "" (Step 19a branding). */
export function useLogoUrl(): string {
  const fromSession = useSessionStore((s) => s.config?.logoUrl)
  const authenticated = useSessionStore((s) => s.status === 'authenticated')
  const { data } = useQuery({
    queryKey: ['version'],
    queryFn: () => api<VersionResponse>('/version', { skipAuthRedirect: true }),
    staleTime: 5 * 60_000,
    enabled: !authenticated,
  })
  if (authenticated) return fromSession ?? ''
  return data?.logoUrl ?? ''
}
