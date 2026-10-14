/** Recent items (SPEC §8.7): derived from the activity journal, 30 days. */
import type { Item } from '@/features/browser/api'
import { api } from '@/lib/api'

export type RecentAction = 'open' | 'edit' | 'upload' | 'create' | 'restore'

export interface RecentItem extends Item {
  location: string
  action: RecentAction
  at: string
}

export interface RecentResponse {
  items: RecentItem[]
  total: number
  windowDays: number
}

export const RECENT_QUERY_KEY = ['recent'] as const

export function listRecent(limit = 100, signal?: AbortSignal): Promise<RecentResponse> {
  return api<RecentResponse>(
    '/me/recent',
    signal ? { query: { limit }, signal } : { query: { limit } },
  )
}
