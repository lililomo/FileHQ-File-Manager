/** Client-side mirror of `fsjail.ValidateName` (SPEC §5.2) for live feedback
 *  in inline rename and the "Baru" dialog. The server stays authoritative. */

export type NameError =
  'empty' | 'dot' | 'slash' | 'backslash' | 'nul' | 'control_char' | 'too_long' | 'exists'

export interface NameCheck {
  error: NameError | null
  /** `windows_incompatible` warning (still allowed). */
  windowsIncompatible: boolean
}

const MAX_NAME_BYTES = 255
const WINDOWS_RESERVED = new Set([
  'CON',
  'PRN',
  'AUX',
  'NUL',
  ...Array.from({ length: 9 }, (_, i) => `COM${i + 1}`),
  ...Array.from({ length: 9 }, (_, i) => `LPT${i + 1}`),
])
const encoder = typeof TextEncoder !== 'undefined' ? new TextEncoder() : null

export function windowsIncompatible(name: string): boolean {
  if (name.endsWith('.') || name.endsWith(' ')) return true
  if (/[<>:"|?*]/.test(name)) return true
  const i = name.indexOf('.')
  const stem = i >= 0 ? name.slice(0, i) : name
  return WINDOWS_RESERVED.has(stem.toUpperCase())
}

/** `siblings` = names already present in the folder (excluding the item itself). */
export function validateName(name: string, siblings: Iterable<string> = []): NameCheck {
  const nfc = name.normalize('NFC')
  const fail = (error: NameError): NameCheck => ({ error, windowsIncompatible: false })
  if (nfc === '') return fail('empty')
  if (nfc === '.' || nfc === '..') return fail('dot')
  for (const ch of nfc) {
    const c = ch.codePointAt(0)!
    if (ch === '/') return fail('slash')
    if (c === 0) return fail('nul')
    if (c < 0x20 || c === 0x7f) return fail('control_char')
    if (ch === '\\') return fail('backslash')
  }
  const bytes = encoder ? encoder.encode(nfc).length : nfc.length
  if (bytes > MAX_NAME_BYTES) return fail('too_long')
  for (const s of siblings) if (s === nfc) return fail('exists')
  return { error: null, windowsIncompatible: windowsIncompatible(nfc) }
}

/** Split "name.tar.gz" into stem and extension for selecting the stem on rename. */
export function splitStem(name: string): { stem: string; ext: string } {
  if (name.startsWith('.') && name.indexOf('.', 1) < 0) return { stem: name, ext: '' }
  const lower = name.toLowerCase()
  for (const compound of ['.tar.gz', '.tar.bz2', '.tar.xz', '.tar.zst', '.tar.lz4']) {
    if (lower.endsWith(compound) && name.length > compound.length)
      return { stem: name.slice(0, -compound.length), ext: name.slice(-compound.length) }
  }
  const i = name.lastIndexOf('.')
  if (i <= 0) return { stem: name, ext: '' }
  return { stem: name.slice(0, i), ext: name.slice(i) }
}
