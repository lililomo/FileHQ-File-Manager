import { QueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api'

/** TanStack Query defaults (SPEC §9.4): staleTime 10 s, one retry, never on 4xx. */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 10_000,
        gcTime: 5 * 60_000,
        refetchOnWindowFocus: false,
        retry: (failureCount, error) => {
          if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false
          return failureCount < 1
        },
      },
      mutations: { retry: 0 },
    },
  })
}

export const queryClient = createQueryClient()
