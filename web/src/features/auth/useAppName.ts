import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { useSessionStore } from '@/stores/session'

interface VersionResponse {
  version: string
  appName: string
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
