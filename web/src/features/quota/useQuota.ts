import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useSessionStore } from '@/stores/session'
import { getQuota, QUOTA_QUERY_KEY, type QuotaUsage } from './api'

/** Keeps `user.quota` in the session store fresh: polled every minute and
 *  invalidated by file operations (`invalidateQuota`). Mount once in the shell. */
export function useQuotaSync(): QuotaUsage | undefined {
  const authenticated = useSessionStore((s) => s.status === 'authenticated')
  const query = useQuery({
    queryKey: QUOTA_QUERY_KEY,
    queryFn: ({ signal }) => getQuota(signal),
    enabled: authenticated,
    staleTime: 15_000,
    refetchInterval: 60_000,
  })
  const data = query.data
  useEffect(() => {
    if (!data) return
    useSessionStore.getState().updateUser({
      quota: { limit: data.limit, used: data.used, trash: data.trash, versions: data.versions },
    })
  }, [data])
  return data
}

export function useInvalidateQuota(): () => void {
  const qc = useQueryClient()
  return () => void qc.invalidateQueries({ queryKey: QUOTA_QUERY_KEY })
}
