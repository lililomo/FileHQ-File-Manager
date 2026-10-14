import { api } from '@/lib/api'

export type ZipMethod = 'store' | 'deflate'

export interface ZipResponse {
  url: string
  expiresAt: string
  name: string
  count: number
}

/** POST /files/zip → signed streaming URL (SPEC §8.9). */
export function createZip(paths: string[], opts: { name?: string; method?: ZipMethod } = {}) {
  return api<ZipResponse>('/files/zip', {
    method: 'POST',
    body: {
      paths,
      ...(opts.name ? { name: opts.name } : {}),
      ...(opts.method ? { method: opts.method } : {}),
    },
  })
}
