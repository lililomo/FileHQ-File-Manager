// Fails if any translation key or {{interpolation}} exists in one language but
// not the other (SPEC §9.8). Run by `pnpm lint` and CI.
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const base = new URL('../src/i18n/', import.meta.url).pathname
const langs = ['id', 'en']
const files = {}
for (const lang of langs) {
  files[lang] = readdirSync(join(base, lang))
    .filter((f) => f.endsWith('.json'))
    .sort()
}
let errors = []
const allFiles = [...new Set(langs.flatMap((l) => files[l]))].sort()
for (const f of allFiles) {
  for (const lang of langs) {
    if (!files[lang].includes(f)) errors.push(`${lang}/${f}: file missing`)
  }
}
function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out)
    else out[key] = String(v)
  }
  return out
}
const interpolations = (s) =>
  [...s.matchAll(/\{\{\s*([\w.]+)\s*\}\}/g)]
    .map((m) => m[1])
    .sort()
    .join(',')
for (const f of allFiles) {
  if (!langs.every((l) => files[l].includes(f))) continue
  const data = Object.fromEntries(
    langs.map((l) => [l, flatten(JSON.parse(readFileSync(join(base, l, f), 'utf8')))]),
  )
  const keys = new Set(langs.flatMap((l) => Object.keys(data[l])))
  for (const key of keys) {
    for (const lang of langs) {
      if (!(key in data[lang])) errors.push(`${lang}/${f}: missing key "${key}"`)
      else if (data[lang][key].trim() === '') errors.push(`${lang}/${f}: empty value for "${key}"`)
    }
    if (langs.every((l) => key in data[l])) {
      const [a, b] = langs.map((l) => interpolations(data[l][key]))
      if (a !== b)
        errors.push(`${f}: interpolation mismatch for "${key}" (${a || '-'} vs ${b || '-'})`)
    }
  }
}
const total = allFiles.length
if (errors.length) {
  console.error(errors.join('\n'))
  console.error(`i18n: ${errors.length} problem(s) in ${total} namespace file(s)`)
  process.exit(1)
}
console.log(`i18n: ${total} namespace file(s) consistent across ${langs.join(', ')}`)
