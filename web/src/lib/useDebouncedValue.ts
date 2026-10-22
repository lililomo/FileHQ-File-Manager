import { useEffect, useState } from 'react'

/** `value`, updated only after it stayed unchanged for `delay` ms (filters). */
export function useDebouncedValue<T>(value: T, delay: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), delay)
    return () => clearTimeout(id)
  }, [value, delay])
  return v
}
