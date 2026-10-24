// Bundle budget (SPEC §1.6 / §11.6, PRD G6, PROMPT Step 21): the JS that
// dist/index.html loads up front (module entry + modulepreload) must stay
// below 300 KB gzip — the build FAILS at or above the budget. Every other
// chunk is lazy and only reported.
//
// Writes bundle-report/bundle-report.json and bundle-report.md (outside dist,
// so the report is never embedded in or served by the binary); `--report`
// also prints the full chunk table. BUNDLE_BUDGET_KB overrides the budget
// (CI uses it to prove the gate fails).
import { existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { gzipSync } from 'node:zlib'
import { join, relative } from 'node:path'

const root = new URL('..', import.meta.url).pathname
const dist = join(root, 'dist')
const outDir = join(root, 'bundle-report')
const indexPath = join(dist, 'index.html')
if (!existsSync(indexPath)) {
  console.error('bundle-size: dist/index.html not found — run `pnpm build` first')
  process.exit(2)
}
const BUDGET = Number(process.env.BUNDLE_BUDGET_KB || 300) * 1024
const printAll = process.argv.includes('--report')

const html = readFileSync(indexPath, 'utf8')
const initial = new Set()
for (const m of html.matchAll(/<script[^>]+type="module"[^>]+src="([^"]+)"/g)) initial.add(m[1])
for (const m of html.matchAll(/<link[^>]+rel="modulepreload"[^>]+href="([^"]+)"/g))
  initial.add(m[1])
const initialCss = new Set()
for (const m of html.matchAll(/<link[^>]+rel="stylesheet"[^>]+href="([^"]+)"/g))
  initialCss.add(m[1])

const walk = (dir) =>
  readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })
const assets = walk(join(dist, 'assets')).filter((p) => /\.(m?js|css|wasm)$/.test(p))

const chunks = assets.map((p) => {
  const raw = readFileSync(p)
  const url = '/' + relative(dist, p)
  const kind = p.endsWith('.css') ? 'css' : p.endsWith('.wasm') ? 'wasm' : 'js'
  return {
    file: url,
    kind,
    initial: initial.has(url) || initialCss.has(url),
    bytes: raw.length,
    gzip: gzipSync(raw, { level: 9 }).length,
  }
})
chunks.sort((a, b) => b.gzip - a.gzip)

const sum = (list) => list.reduce((n, c) => n + c.gzip, 0)
const initialJs = chunks.filter((c) => c.initial && c.kind === 'js')
const report = {
  budget: BUDGET,
  initialJsGzip: sum(initialJs),
  initialCssGzip: sum(chunks.filter((c) => c.initial && c.kind === 'css')),
  lazyJsGzip: sum(chunks.filter((c) => !c.initial && c.kind === 'js')),
  initialChunks: initialJs.length,
  totalChunks: chunks.length,
  chunks,
}

const kb = (n) => (n / 1024).toFixed(1) + ' KB'
const missing = [...initial].filter((u) => !chunks.some((c) => c.file === u))
if (missing.length) {
  console.error(
    `bundle-size: index.html references files not in dist/assets: ${missing.join(', ')}`,
  )
  process.exit(2)
}
for (const c of chunks.filter((c) => c.initial || printAll))
  console.log(`${kb(c.gzip).padStart(10)}  ${c.initial ? 'initial' : 'lazy   '}  ${c.file}`)
console.log(
  `initial JS gzip: ${kb(report.initialJsGzip)} in ${report.initialChunks} chunks (budget < ${kb(BUDGET)}); ` +
    `initial CSS gzip: ${kb(report.initialCssGzip)}; lazy JS gzip: ${kb(report.lazyJsGzip)} in ${chunks.filter((c) => !c.initial && c.kind === 'js').length} chunks`,
)

mkdirSync(outDir, { recursive: true })
writeFileSync(join(outDir, 'bundle-report.json'), JSON.stringify(report, null, 2) + '\n')
const md = [
  '# Bundle report',
  '',
  `Initial JS gzip **${kb(report.initialJsGzip)}** (budget < ${kb(BUDGET)}), initial CSS gzip ${kb(report.initialCssGzip)}, lazy JS gzip ${kb(report.lazyJsGzip)}.`,
  '',
  '| gzip | raw | load | file |',
  '| ---: | ---: | --- | --- |',
  ...chunks.map(
    (c) => `| ${kb(c.gzip)} | ${kb(c.bytes)} | ${c.initial ? 'initial' : 'lazy'} | \`${c.file}\` |`,
  ),
  '',
].join('\n')
writeFileSync(join(outDir, 'bundle-report.md'), md)

if (report.initialJsGzip >= BUDGET) {
  console.error(
    `bundle-size: initial JS ${kb(report.initialJsGzip)} is at or above the budget ${kb(BUDGET)} (SPEC §1.6)`,
  )
  process.exit(1)
}
