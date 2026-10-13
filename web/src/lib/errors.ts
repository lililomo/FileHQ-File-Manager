import i18next from 'i18next'
import { ApiError } from '@/lib/api'

/** Human message for an API error code from the `errors` catalog (SPEC §9.8: client-side translation wins). */
export function errorMessage(codeOrError: string | unknown, fallback?: string): string {
  const code =
    typeof codeOrError === 'string'
      ? codeOrError
      : codeOrError instanceof ApiError
        ? codeOrError.code
        : 'UNKNOWN'
  const key = `errors:${code}`
  if (i18next.exists(key)) return String(i18next.t(key as 'errors:UNKNOWN'))
  if (fallback) return fallback
  if (codeOrError instanceof ApiError && codeOrError.message) return codeOrError.message
  return String(i18next.t('errors:UNKNOWN'))
}

/** Translate a validation reason ("too_short", "slash", …) if known. */
export function reasonMessage(reason: string, values?: Record<string, unknown>): string {
  const key = `errors:reason.${reason}`
  if (i18next.exists(key)) return String(i18next.t(key as 'errors:reason.required', values ?? {}))
  return reason
}
