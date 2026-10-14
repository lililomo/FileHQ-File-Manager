export const WHO = ['owner', 'group', 'public'] as const
export const BITS = ['r', 'w', 'x'] as const
export type Who = (typeof WHO)[number]
export type Bit = (typeof BITS)[number]

/** "0644" → 9 booleans (setuid/setgid/sticky are dropped, SPEC §8.5). */
export function octalToBits(octal: string): Record<Who, Record<Bit, boolean>> {
  const digits = octal
    .replace(/[^0-7]/g, '')
    .slice(-3)
    .padStart(3, '0')
  const out = {} as Record<Who, Record<Bit, boolean>>
  WHO.forEach((who, i) => {
    const d = Number.parseInt(digits[i]!, 8)
    out[who] = { r: (d & 4) !== 0, w: (d & 2) !== 0, x: (d & 1) !== 0 }
  })
  return out
}

export function bitsToOctal(bits: Record<Who, Record<Bit, boolean>>): string {
  return (
    '0' +
    WHO.map((who) => {
      const b = bits[who]
      return String((b.r ? 4 : 0) + (b.w ? 2 : 0) + (b.x ? 1 : 0))
    }).join('')
  )
}
