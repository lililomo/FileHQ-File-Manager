// WCAG 2.x contrast check for the design tokens (SPEC §9.1).
// Parses src/styles/tokens.css, resolves each theme × accent context and
// verifies the pairs below. Exit code 1 on any failure. No dependencies.
import { readFileSync } from 'node:fs'

const css = readFileSync(new URL('../src/styles/tokens.css', import.meta.url), 'utf8')

const blocks = {}
for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
  const selector = m[1].trim().split('\n').pop().trim()
  const vars = {}
  for (const v of m[2].matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) vars[v[1]] = v[2].trim()
  if (Object.keys(vars).length) blocks[selector] = { ...(blocks[selector] ?? {}), ...vars }
}

const ACCENTS = ['blue', 'emerald', 'violet', 'amber', 'rose', 'slate']
const contexts = []
for (const theme of ['light', 'dark']) {
  for (const accent of ACCENTS) {
    const selectors = [':root']
    if (theme === 'dark') selectors.push("[data-theme='dark']")
    if (accent !== 'blue') {
      selectors.push(`[data-accent='${accent}']`)
      if (theme === 'dark') selectors.push(`[data-theme='dark'][data-accent='${accent}']`)
    }
    const vars = Object.assign({}, ...selectors.map((s) => blocks[s] ?? {}))
    contexts.push({ name: `${theme}/${accent}`, vars })
  }
}

// [foreground, background, minimum ratio, kind]
const PAIRS = [
  ['--text', '--bg', 4.5, 'text'],
  ['--text-muted', '--bg', 4.5, 'text'],
  ['--text', '--surface', 4.5, 'text'],
  ['--text', '--surface-2', 4.5, 'text'],
  ['--text-muted', '--surface', 4.5, 'text'],
  ['--accent-fg', '--accent', 4.5, 'text on accent (buttons)'],
  ['--text', '--accent-subtle', 4.5, 'text on selected row'],
  ['--danger', '--bg', 4.5, 'error text'],
  ['--accent', '--bg', 3, 'ui'],
  ['--accent', '--surface', 3, 'ui'],
  ['--focus', '--bg', 3, 'focus ring'],
  ['--success', '--bg', 4.5, 'status text (text-success)'],
  ['--success', '--surface', 4.5, 'status text on surface'],
  ['--warning', '--bg', 4.5, 'status text (text-warning)'],
  ['--warning', '--surface', 4.5, 'status text on surface'],
  ['--danger', '--surface', 3, 'ui'],
]

function resolve(vars, name, depth = 0) {
  const raw = vars[name]
  if (raw == null) throw new Error(`token ${name} missing`)
  const ref = raw.match(/^var\((--[\w-]+)\)$/)
  if (ref) {
    if (depth > 5) throw new Error(`token ${name} loops`)
    return resolve(vars, ref[1], depth + 1)
  }
  return raw
}
function hexToRgb(hex) {
  const h = hex.replace('#', '')
  const full =
    h.length === 3
      ? h
          .split('')
          .map((c) => c + c)
          .join('')
      : h
  if (!/^[0-9a-f]{6}$/i.test(full)) throw new Error(`not a hex colour: ${hex}`)
  return [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16))
}
function luminance([r, g, b]) {
  const lin = [r, g, b].map((c) => {
    const s = c / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2]
}
export function contrast(fgHex, bgHex) {
  const a = luminance(hexToRgb(fgHex))
  const b = luminance(hexToRgb(bgHex))
  const [hi, lo] = a > b ? [a, b] : [b, a]
  return (hi + 0.05) / (lo + 0.05)
}

let failures = 0
let checks = 0
const rows = []
for (const ctx of contexts) {
  for (const [fg, bg, min, kind] of PAIRS) {
    const ratio = contrast(resolve(ctx.vars, fg), resolve(ctx.vars, bg))
    checks++
    const ok = ratio >= min
    if (!ok) failures++
    if (!ok || process.argv.includes('--verbose')) {
      rows.push(
        `${ok ? 'ok  ' : 'FAIL'} ${ctx.name.padEnd(14)} ${fg} on ${bg} = ${ratio.toFixed(2)}:1 (min ${min}, ${kind})`,
      )
    }
  }
}
if (rows.length) console.log(rows.join('\n'))
console.log(
  `contrast: ${checks - failures}/${checks} pairs pass across ${contexts.length} theme/accent contexts`,
)
if (failures) {
  console.error(`contrast: ${failures} pair(s) below WCAG AA`)
  process.exit(1)
}
