// Copies the pdf.js runtime assets that are fetched by URL at run time
// (CMaps for CJK fonts, the 14 standard fonts, the WASM decoders for
// JPX/JBIG2/ICC, annotation icons) from node_modules into public/pdfjs so
// they ship inside the embedded dist (SPEC §8.14 PdfViewer, Step 17a). The
// worker itself is bundled by Vite (`?url` import). Idempotent: skipped when
// public/pdfjs/VERSION already matches the installed pdfjs-dist.
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'

const require = createRequire(import.meta.url)
const pkg = require('pdfjs-dist/package.json')
const src = dirname(require.resolve('pdfjs-dist/package.json'))
const dest = new URL('../public/pdfjs/', import.meta.url).pathname
const marker = join(dest, 'VERSION')
if (existsSync(marker) && readFileSync(marker, 'utf8').trim() === pkg.version) process.exit(0)
rmSync(dest, { recursive: true, force: true })
mkdirSync(dest, { recursive: true })
for (const [from, to] of [
  ['cmaps', 'cmaps'],
  ['standard_fonts', 'standard_fonts'],
  ['wasm', 'wasm'],
  ['iccs', 'iccs'],
  ['web/images', 'images'],
]) {
  const s = join(src, from)
  if (!existsSync(s)) continue
  cpSync(s, join(dest, to), { recursive: true })
}
writeFileSync(marker, pkg.version + '\n')
console.log(`pdfjs-assets: copied pdfjs-dist ${pkg.version} runtime files to public/pdfjs`)
