/** Form model of `/admin/settings` (Step 19a): the server describes every
 *  key (type, bounds, options); this file turns that list into a zod schema
 *  and converts between form strings and API values. */
import { z } from 'zod'
import type { AdminSetting, SettingKind, SettingValue } from './api'

export type FormValues = Record<string, string | boolean>

const SIZE_RE = /^\s*(\d+(?:\.\d+)?)\s*([kmgt]?b?)\s*$/i
const UNIT: Record<string, number> = {
  '': 1,
  b: 1,
  k: 1024,
  kb: 1024,
  m: 1024 ** 2,
  mb: 1024 ** 2,
  g: 1024 ** 3,
  gb: 1024 ** 3,
  t: 1024 ** 4,
  tb: 1024 ** 4,
}

/** Parses "50GB" / "16 MB" / "123" into bytes (null when invalid). */
export function parseSize(s: string): number | null {
  const m = SIZE_RE.exec(s)
  if (!m) return null
  const n = Number(m[1])
  const unit = UNIT[(m[2] ?? '').toLowerCase()]
  if (!Number.isFinite(n) || unit === undefined) return null
  return Math.round(n * unit)
}

const DURATION_RE = /^\s*(\d+)\s*(ns|us|µs|ms|s|m|h|d)\s*$/
const DUR_SECONDS: Record<string, number> = { s: 1, m: 60, h: 3600, d: 86400 }

/** Parses "2h" / "30m" / "7d" into seconds (null when invalid or sub-second). */
export function parseDurationSeconds(s: string): number | null {
  const parts = s.trim().match(/\d+\s*(?:ns|us|µs|ms|s|m|h|d)/g)
  if (!parts || parts.join('') !== s.replace(/\s+/g, '')) return null
  let total = 0
  for (const p of parts) {
    const m = DURATION_RE.exec(p)
    if (!m) return null
    const mult = DUR_SECONDS[m[2] ?? '']
    if (mult === undefined) return null
    total += Number(m[1]) * mult
  }
  return total > 0 ? total : null
}

/** The form string of an API value. */
export function toForm(item: AdminSetting): string | boolean {
  if (item.type === 'bool') return item.value === true
  if (item.type === 'list')
    return Array.isArray(item.value) ? item.value.join(', ') : String(item.value)
  return String(item.value)
}

/** The API value of a form string. */
export function toApi(kind: SettingKind, v: string | boolean): SettingValue {
  if (kind === 'bool') return v === true
  const s = String(v).trim()
  if (kind === 'int') return Number(s)
  if (kind === 'list')
    return s
      .split(/[,\n;]/)
      .map((x) => x.trim())
      .filter(Boolean)
  return s
}

/** Builds the zod schema from the server description (same bounds as the server). */
export function buildSchema(items: AdminSetting[]) {
  const shape: Record<string, z.ZodTypeAny> = {}
  for (const it of items) {
    if (it.readOnly) continue
    const v = it.validation ?? {}
    switch (it.type) {
      case 'bool':
        shape[it.key] = z.boolean()
        break
      case 'int':
        shape[it.key] = z
          .string()
          .refine((s) => /^-?\d+$/.test(s.trim()), 'invalid')
          .refine((s) => v.min === undefined || Number(s) >= v.min, 'min')
          .refine((s) => v.max === undefined || Number(s) <= v.max, 'max')
        break
      case 'size':
        shape[it.key] = z
          .string()
          .refine((s) => parseSize(s) !== null, 'invalid')
          .refine((s) => v.min === undefined || (parseSize(s) ?? 0) >= v.min, 'min')
          .refine((s) => v.max === undefined || (parseSize(s) ?? 0) <= v.max, 'max')
        break
      case 'duration':
        shape[it.key] = z
          .string()
          .refine((s) => parseDurationSeconds(s) !== null, 'invalid')
          .refine((s) => v.min === undefined || (parseDurationSeconds(s) ?? 0) >= v.min, 'min')
          .refine((s) => v.max === undefined || (parseDurationSeconds(s) ?? 0) <= v.max, 'max')
        break
      case 'enum':
        shape[it.key] = z.string().refine((s) => (v.options ?? []).includes(s), 'invalid')
        break
      case 'string':
        shape[it.key] = z
          .string()
          .trim()
          .min(1, 'required')
          .max(v.maxLen ?? 1000, 'max')
        break
      case 'tz':
        shape[it.key] = z.string().trim().min(1, 'required').max(64, 'max')
        break
      case 'list':
        shape[it.key] = z.string()
        break
    }
  }
  return z.object(shape)
}

/** Keys whose form value differs from the server's current value. */
export function changedKeys(items: AdminSetting[], values: FormValues): string[] {
  const out: string[] = []
  for (const it of items) {
    if (it.readOnly) continue
    const cur = toForm(it)
    const next = values[it.key]
    if (next === undefined) continue
    if (typeof cur === 'boolean' ? cur !== next : String(cur).trim() !== String(next).trim())
      out.push(it.key)
  }
  return out
}

export const GROUP_ORDER = [
  'upload',
  'storage',
  'trash',
  'remote',
  'sharing',
  'security',
  'tasks',
  'media',
  'appearance',
] as const
