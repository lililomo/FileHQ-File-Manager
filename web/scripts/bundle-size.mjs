// Measures the gzip size of the initial JS/CSS loaded by dist/index.html
// (SPEC §1.6 / PRD G6: initial JS < 300 KB gzip). Reports always; fails only
// when BUNDLE_BUDGET_STRICT=1 (enforced from Step 21).
import { readFileSync, writeFileSync, existsSync } from 'node:fs'
import { gzipSync } from 'node:zlib'
import { join } from 'node:path'

const dist = new URL('../dist/', import.meta.url).pathname
const indexPath = join(dist, 'index.html')
if (!existsSync(indexPath)) {
  console.error('bundle-size: dist/index.html not found — run `pnpm build` first')
  process.exit(2)
}
const html = readFileSync(indexPath, 'utf8')
const js = new Set()
const cssFiles = new Set()
for (const m of html.matchAll(/<script[^>]+type="module"[^>]+src="([^"]+)"/g)) js.add(m[1])
for (const m of html.matchAll(/<link[^>]+rel="modulepreload"[^>]+href="([^"]+)"/g)) js.add(m[1])
for (const m of html.matchAll(/<link[^>]+rel="stylesheet"[^>]+href="([^"]+)"/g)) cssFiles.add(m[1])

const BUDGET = 300 * 1024
const gz = (rel) => gzipSync(readFileSync(join(dist, rel.replace(/^\//, ''))), { level: 9 }).length
const report = { js: [], css: [], jsGzipTotal: 0, cssGzipTotal: 0, budget: BUDGET }
for (const f of js) {
  const size = gz(f)
  report.js.push({ file: f, gzip: size })
  report.jsGzipTotal += size
}
for (const f of cssFiles) {
  const size = gz(f)
  report.css.push({ file: f, gzip: size })
  report.cssGzipTotal += size
}
const kb = (n) => (n / 1024).toFixed(1) + ' KB'
for (const r of [...report.js, ...report.css]) console.log(`${kb(r.gzip).padStart(10)}  ${r.file}`)
console.log(
  `initial JS gzip: ${kb(report.jsGzipTotal)} (budget ${kb(BUDGET)}); CSS gzip: ${kb(report.cssGzipTotal)}`,
)
writeFileSync(join(dist, 'bundle-size.json'), JSON.stringify(report, null, 2))
if (report.jsGzipTotal > BUDGET) {
  const msg = `bundle-size: initial JS ${kb(report.jsGzipTotal)} exceeds budget ${kb(BUDGET)}`
  if (process.env.BUNDLE_BUDGET_STRICT === '1') {
    console.error(msg)
    process.exit(1)
  }
  console.warn(msg + ' (warning; strict mode starts in Step 21)')
}
