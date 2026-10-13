import { useQuery } from '@tanstack/react-query'
import { statItem } from './api'

/** Shared "stat" query for the preview panel, Quick Look and Properties. */
export function useItemStat(path: string | null) {
  return useQuery({
    queryKey: ['stat', path],
    queryFn: ({ signal }) => statItem(path!, signal),
    enabled: !!path,
    staleTime: 10_000,
  })
}
