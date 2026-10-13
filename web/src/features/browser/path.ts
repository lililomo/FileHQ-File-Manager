/** Virtual path helpers for the browser (SPEC §5.1): `/` = user root, no trailing slash, NFC. */

export const ROOT = '/'

/** Segments of a virtual path, without the root. */
export function splitPath(path: string): string[] {
  return path.split('/').filter((s) => s.length > 0)
}

export function joinPath(dir: string, name: string): string {
  if (dir === ROOT || dir === '') return `/${name}`
  return `${dir}/${name}`
}

export function parentPath(path: string): string | null {
  const parts = splitPath(path)
  if (parts.length === 0) return null
  parts.pop()
  return parts.length === 0 ? ROOT : `/${parts.join('/')}`
}

export function baseName(path: string): string {
  const parts = splitPath(path)
  return parts[parts.length - 1] ?? ''
}

/** Route splat (`files/$`) → virtual path. */
export function splatToPath(splat: string | undefined): string {
  if (!splat) return ROOT
  const parts = splitPath(splat)
  return parts.length === 0 ? ROOT : `/${parts.join('/')}`
}

/** Virtual path → route splat (no leading slash). */
export function pathToSplat(path: string): string {
  return splitPath(path).join('/')
}

export type TypedPathResult = { ok: true; path: string } | { ok: false; reason: string }

// eslint-disable-next-line no-control-regex -- control characters are exactly what we reject (SPEC §5.1)
const CONTROL = /[\u0000-\u001f\u007f]/

/** Normalise a path typed into the address bar. Relative input is resolved
 *  against `base`; `..` is rejected (SPEC §5.1 never resolves parent segments). */
export function normalizeTypedPath(input: string, base: string = ROOT): TypedPathResult {
  const raw = input.trim().normalize('NFC')
  if (CONTROL.test(raw)) return { ok: false, reason: 'control_char' }
  const absolute = raw.startsWith('/')
  const combined = absolute ? raw : joinPath(base, raw)
  const parts: string[] = []
  for (const seg of combined.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') return { ok: false, reason: 'parent_segment' }
    if (seg.length > 255) return { ok: false, reason: 'name_too_long' }
    parts.push(seg)
  }
  if (parts[0] === '.system') return { ok: false, reason: 'reserved' }
  const path = parts.length === 0 ? ROOT : `/${parts.join('/')}`
  if (path.length > 4096) return { ok: false, reason: 'path_too_long' }
  return { ok: true, path }
}
