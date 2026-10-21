import { api } from '@/lib/api'
import { isPublicScope, publicApiPath } from '@/lib/public-scope'

export type ZipMethod = 'store' | 'deflate'

export interface ZipResponse {
  url: string
  expiresAt: string
  name: string
  count: number
}

/** POST /files/zip → signed streaming URL (SPEC §8.9). */
export function createZip(paths: string[], opts: { name?: string; method?: ZipMethod } = {}) {
  return api<ZipResponse>(isPublicScope() ? publicApiPath('zip') : '/files/zip', {
    method: 'POST',
    body: {
      paths,
      ...(opts.name ? { name: opts.name } : {}),
      ...(opts.method ? { method: opts.method } : {}),
    },
  })
}
