/** Locale-aware formatting helpers (SPEC §9.8). `locale` is the UI language ("id" | "en"). */

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const

// Intl constructors are expensive (≈ 0.5–2 ms each); listings call these per cell,
// so instances are cached per locale + options.
const cache = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat>()
function cached<T extends Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat>(
  key: string,
  make: () => T,
): T {
  let inst = cache.get(key) as T | undefined
  if (!inst) {
    inst = make()
    cache.set(key, inst)
  }
  return inst
}

export function toIntlLocale(language: string): string {
  if (language === 'id') return 'id-ID'
  if (language === 'en') return 'en-GB'
  return language
}

/** 1024-based, one decimal below 10 ("1,2 MB" in id, "1.2 MB" in en), none above ("24 KB"). */
export function formatBytes(bytes: number, language = 'id'): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—'
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit += 1
  }
  const digits = unit === 0 ? 0 : value < 10 ? 1 : 0
  const n = cached(
    `bytes:${language}:${digits}`,
    () => new Intl.NumberFormat(toIntlLocale(language), { maximumFractionDigits: digits }),
  ).format(value)
  return `${n} ${UNITS[unit]}`
}

export function formatNumber(
  n: number,
  language = 'id',
  options?: Intl.NumberFormatOptions,
): string {
  return cached(
    `num:${language}:${JSON.stringify(options ?? {})}`,
    () => new Intl.NumberFormat(toIntlLocale(language), options),
  ).format(n)
}

export function formatPercent(ratio: number, language = 'id'): string {
  return cached(
    `pct:${language}`,
    () =>
      new Intl.NumberFormat(toIntlLocale(language), { style: 'percent', maximumFractionDigits: 0 }),
  ).format(ratio)
}

function toDate(input: string | number | Date): Date | null {
  const d = input instanceof Date ? input : new Date(input)
  return Number.isNaN(d.getTime()) ? null : d
}

let displayTimeZone: string | undefined

/** Display time zone from the `ui.timezone` preference (SPEC §8.18);
 *  "auto"/empty/unknown = the browser's zone. Returns the zone applied. */
export function setDisplayTimeZone(tz: unknown): string | undefined {
  displayTimeZone = undefined
  if (typeof tz === 'string' && tz !== '' && tz !== 'auto') {
    try {
      new Intl.DateTimeFormat('en', { timeZone: tz })
      displayTimeZone = tz
    } catch {
      /* unknown zone: keep the browser's */
    }
  }
  return displayTimeZone
}

/** "12 Okt 2026" / "12 Oct 2026"; with `time`: "12 Okt 2026, 14.05". */
export function formatDate(
  input: string | number | Date,
  language = 'id',
  options: { time?: boolean; timeZone?: string } = {},
): string {
  const d = toDate(input)
  if (!d) return '—'
  const loc = toIntlLocale(language)
  const zone = options.timeZone ?? displayTimeZone
  const tz = zone ? { timeZone: zone } : {}
  const tzKey = zone ?? ''
  const date = cached(
    `date:${loc}:${tzKey}`,
    () => new Intl.DateTimeFormat(loc, { day: 'numeric', month: 'short', year: 'numeric', ...tz }),
  ).format(d)
  if (!options.time) return date
  const time = cached(
    `time:${loc}:${tzKey}`,
    () => new Intl.DateTimeFormat(loc, { hour: '2-digit', minute: '2-digit', ...tz }),
  ).format(d)
  return `${date}, ${time}`
}

/** Full absolute form for tooltips: "12 Okt 2026, 14.05.09". */
export function formatDateTimeFull(input: string | number | Date, language = 'id'): string {
  const d = toDate(input)
  if (!d) return '—'
  const zone = displayTimeZone
  return cached(
    `full:${language}:${zone ?? ''}`,
    () =>
      new Intl.DateTimeFormat(toIntlLocale(language), {
        dateStyle: 'medium',
        timeStyle: 'medium',
        ...(zone ? { timeZone: zone } : {}),
      }),
  ).format(d)
}

/** Relative within 7 days ("2 jam yang lalu", "kemarin"), absolute date beyond. */
export function formatRelativeTime(
  input: string | number | Date,
  language = 'id',
  now: number = Date.now(),
): string {
  const d = toDate(input)
  if (!d) return '—'
  const diffSec = Math.round((d.getTime() - now) / 1000)
  const abs = Math.abs(diffSec)
  const rtf = cached(
    `rtf:${language}`,
    () => new Intl.RelativeTimeFormat(toIntlLocale(language), { numeric: 'auto' }),
  )
  if (abs < 45) return rtf.format(0, 'second')
  if (abs < 3600) return rtf.format(Math.round(diffSec / 60), 'minute')
  if (abs < 86_400) return rtf.format(Math.round(diffSec / 3600), 'hour')
  if (abs < 7 * 86_400) return rtf.format(Math.round(diffSec / 86_400), 'day')
  return formatDate(d, language)
}

/** `h:mm:ss` / `m:ss` for media durations (seconds). */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  const mm = h > 0 ? String(m).padStart(2, '0') : String(m)
  return `${h > 0 ? `${h}:` : ''}${mm}:${String(sec).padStart(2, '0')}`
}
