/** Quota API (GET /me/quota, SPEC §8.2). */
import { api } from '@/lib/api'

export interface QuotaUsage {
  quota: number
  limit: number
  used: number
  trash: number
  versions: number
  reserved: number
  free: number
  percent: number
  fileCount?: number
  dirCount?: number
  dirty: boolean
  calculatedAt: string | null
}

export function getQuota(signal?: AbortSignal): Promise<QuotaUsage> {
  return api<QuotaUsage>('/me/quota', signal ? { signal } : {})
}

export const QUOTA_QUERY_KEY = ['quota'] as const

/** SPEC §8.2 indicator thresholds. */
export const QUOTA_WARN_PERCENT = 80
export const QUOTA_NEAR_FULL_PERCENT = 90
export const QUOTA_DANGER_PERCENT = 95

export function quotaPercent(limit: number, used: number): number {
  if (limit <= 0) return 0
  return Math.min(100, Math.round((used / limit) * 100))
}
