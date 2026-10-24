import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ListOptions, ListResponse } from './api'

const calls: ListOptions[] = []
vi.mock('./api', async (orig) => {
  const real = await orig<typeof import('./api')>()
  return {
    ...real,
    listDir: vi.fn((_path: string, opts: ListOptions = {}) => {
      calls.push(opts)
      const res: ListResponse = {
        items: [],
        nextCursor: null,
        total: opts.media ? 2 : 1,
        path: '/',
        parentPath: null,
        dir: { path: '/', etag: 'e', mtime: '2026-10-24T00:00:00Z', itemCount: 1 },
      } as unknown as ListResponse
      return Promise.resolve(res)
    }),
  }
})

const { useListing } = await import('./useListing')

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

afterEach(() => {
  calls.length = 0
})

describe('useListing media enrichment (Step 21)', () => {
  it('lists without media first and enriches when idle, keeping the rows', async () => {
    const { result } = renderHook(() => useListing('/foto', { media: true }), { wrapper })
    await waitFor(() => expect(calls.length).toBe(2))
    expect(calls[0]!.media).toBe(false)
    expect(calls[1]!.media).toBe(true)
    await waitFor(() => expect(result.current.total).toBe(2))
  })

  it('never asks for media when the columns are hidden', async () => {
    const { result } = renderHook(() => useListing('/foto', {}), { wrapper })
    await waitFor(() => expect(result.current.total).toBe(1))
    await new Promise((r) => setTimeout(r, 300))
    expect(calls.map((c) => c.media)).toEqual([false])
  })
})
