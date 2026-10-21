import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { apiMkdir, apiResetPrefs, e2eStorageRoot, loginAsAdmin } from './fixtures'
import { makePdf } from './pdf-fixtures'

test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/galeri'
const VIPS = '/usr/bin/vips'
const SVG_WITH_SCRIPT =
  '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="90"><script>window.__xss=1</script><rect width="160" height="90" fill="#16a34a"/></svg>'

// --- EXIF fixture: a little-endian TIFF with Make/Model/Orientation and a GPS IFD,
// spliced into the JPEG that vips produced (same layout as the Go test fixtures) ---
function u16(v: number): number[] {
  return [v & 0xff, (v >> 8) & 0xff]
}
function u32(v: number): number[] {
  return [v & 0xff, (v >> 8) & 0xff, (v >> 16) & 0xff, (v >>> 24) & 0xff]
}
function rational(...pairs: [number, number][]): number[] {
  return pairs.flatMap(([n, d]) => [...u32(n), ...u32(d)])
}
interface Entry {
  tag: number
  type: number
  count: number
  inline?: number[]
  out?: number[]
}
function exifTIFF(): Buffer {
  const make = [...Buffer.from('Canon\0')]
  const model = [...Buffer.from('EOS R6\0')]
  const ifd0: Entry[] = [
    { tag: 0x010f, type: 2, count: make.length, out: make },
    { tag: 0x0110, type: 2, count: model.length, out: model },
    { tag: 0x0112, type: 3, count: 1, inline: [...u16(1), 0, 0] },
  ]
  const gps: Entry[] = [
    { tag: 1, type: 2, count: 2, inline: [0x53, 0, 0, 0] }, // S
    { tag: 2, type: 5, count: 3, out: rational([8, 1], [13, 1], [12, 1]) },
    { tag: 3, type: 2, count: 2, inline: [0x45, 0, 0, 0] }, // E
    { tag: 4, type: 5, count: 3, out: rational([114, 1], [22, 1], [30, 1]) },
  ]
  const ifd0Size = 2 + 12 * (ifd0.length + 1) + 4
  const gpsOff = 8 + ifd0Size
  const gpsSize = 2 + 12 * gps.length + 4
  const valuesOff = gpsOff + gpsSize
  ifd0.push({ tag: 0x8825, type: 4, count: 1, inline: u32(gpsOff) })
  const head: number[] = [0x49, 0x49, ...u16(42), ...u32(8)]
  const values: number[] = []
  const writeIFD = (entries: Entry[]) => {
    const b: number[] = [...u16(entries.length)]
    for (const e of entries) {
      b.push(...u16(e.tag), ...u16(e.type), ...u32(e.count))
      if (e.out) {
        b.push(...u32(valuesOff + values.length))
        values.push(...e.out)
      } else b.push(...e.inline!)
    }
    b.push(...u32(0))
    return b
  }
  const body = [...writeIFD(ifd0), ...writeIFD(gps)]
  return Buffer.from([...head, ...body, ...values])
}
function spliceExif(jpeg: Buffer): Buffer {
  const payload = Buffer.concat([Buffer.from('Exif\0\0'), exifTIFF()])
  const app1 = Buffer.from([0xff, 0xe1, (payload.length + 2) >> 8, (payload.length + 2) & 0xff])
  return Buffer.concat([jpeg.subarray(0, 2), app1, payload, jpeg.subarray(2)])
}

let seeded = false
function seed(dir: string) {
  if (seeded) return
  seeded = true
  mkdirSync(dir, { recursive: true })
  // large enough that "fit" is below 100 % (the original only loads at ≥ 100 %)
  execFileSync(VIPS, ['black', join(dir, 'a-kamera.jpg'), '3000', '2000'])
  writeFileSync(join(dir, 'a-kamera.jpg'), spliceExif(readFileSync(join(dir, 'a-kamera.jpg'))))
  execFileSync(VIPS, ['black', join(dir, 'b-foto.png'), '320', '240'])
  execFileSync(VIPS, ['black', join(dir, 'c-anim.gif'), '32', '32'])
  writeFileSync(join(dir, 'd-logo.svg'), SVG_WITH_SCRIPT)
  writeFileSync(join(dir, 'e-catatan.txt'), 'bukan gambar')
}

const cspErrors: string[] = []
test.beforeEach(async ({ page }) => {
  test.skip(!existsSync(VIPS), 'vips not installed')
  page.on('console', (m) => {
    if (m.type() === 'error' && /Content Security Policy|worker/i.test(m.text()))
      cspErrors.push(m.text())
  })
  await loginAsAdmin(page)
  await page.waitForTimeout(700)
  await apiResetPrefs(page.request)
  if (!seeded) {
    await apiMkdir(page.request, '/', 'galeri')
    seed(join(e2eStorageRoot(), 'admin', 'galeri'))
  }
})

async function openGallery(page: Page) {
  await page.goto(`/files${DIR}`)
  await expect(page.getByRole('row', { name: /a-kamera\.jpg/ })).toBeVisible()
}

const overlay = (page: Page) => page.getByTestId('viewer-overlay')
const viewerName = (page: Page) => page.getByTestId('viewer-name')

test('image viewer: preview first, original at 1:1, rotate, prev/next, slideshow', async ({
  page,
}) => {
  await openGallery(page)
  await page.getByRole('row', { name: /a-kamera\.jpg/ }).dblclick()
  await expect(overlay(page)).toBeVisible()
  await expect(viewerName(page)).toHaveText('a-kamera.jpg')
  // 4 images in the folder (jpg, png, gif, svg); the txt is not a sibling
  await expect(page.getByTestId('viewer-position')).toHaveText('1 / 4')
  await expect(page).toHaveURL(/view=%2Fgaleri%2Fa-kamera\.jpg/)

  const img = page.getByTestId('viewer-image')
  await expect(img).toBeVisible()
  await expect(img).toHaveAttribute('src', /variant=preview/)
  await expect(page.getByTestId('viewer-canvas')).toHaveAttribute('data-source', 'preview')

  // fit is below 100 % for a 3000×2000 photo; "+" zooms in by a step
  const zoomOf = async () =>
    Number.parseInt((await page.getByTestId('viewer-zoom').innerText()) || '0', 10)
  await expect.poll(zoomOf).toBeLessThan(100)
  const fitZoom = await zoomOf()
  await page.keyboard.press('+')
  await expect.poll(zoomOf).toBeGreaterThan(fitZoom)
  // 1:1 fetches the original (no variant)
  const originalReq = page.waitForRequest(
    (r) => r.url().includes('/api/v1/files/raw') && !r.url().includes('variant='),
  )
  await page.getByTestId('viewer-original').click()
  await originalReq
  await expect(page.getByTestId('viewer-zoom')).toHaveText('100%')
  await expect(page.getByTestId('viewer-canvas')).toHaveAttribute('data-source', 'original')
  await expect(img).toHaveAttribute('src', /purpose=media/)
  await expect(img).not.toHaveAttribute('src', /variant=/)

  // rotate with R is a display transform only
  await page.keyboard.press('r')
  await expect(img).toHaveCSS('transform', /matrix/) // rotated matrix, not "none"
  const transform = await img.evaluate((el) => el.style.transform)
  expect(transform).toContain('rotate(90deg)')

  // next / prev among images only (the .txt is skipped)
  await page.keyboard.press('ArrowRight')
  await expect(viewerName(page)).toHaveText('b-foto.png')
  await expect(page.getByTestId('viewer-position')).toHaveText('2 / 4')
  await page.getByTestId('viewer-prev').click()
  await expect(viewerName(page)).toHaveText('a-kamera.jpg')
  await expect(page.getByTestId('viewer-prev')).toBeDisabled()
  // filmstrip reflects the siblings and jumps on click
  await expect(page.getByTestId('filmstrip-item')).toHaveCount(4)
  await page.getByTestId('filmstrip-item').nth(2).click()
  await expect(viewerName(page)).toHaveText('c-anim.gif')
  // the GIF is shown from the original so it animates
  await expect(page.getByTestId('viewer-canvas')).toHaveAttribute('data-source', 'original')

  // slideshow with a 2 s interval advances on its own
  await page.getByTestId('viewer-interval').click()
  await page.getByRole('option', { name: '2 detik' }).click()
  await page.getByTestId('viewer-slideshow').click()
  await expect(viewerName(page)).toHaveText('d-logo.svg', { timeout: 4_000 })
  await expect(viewerName(page)).toHaveText('a-kamera.jpg', { timeout: 4_000 }) // wraps around
  await page.getByTestId('viewer-slideshow').click()

  // Esc closes; focus returns to the grid row that opened it
  await page.keyboard.press('Escape')
  await expect(overlay(page)).toBeHidden()
  await expect(page).not.toHaveURL(/view=/)
  const focusedInGrid = await page.evaluate(
    () => !!document.activeElement?.closest('[data-testid="browser"] [role="grid"]'),
  )
  expect(focusedInGrid).toBe(true)
  expect(cspErrors).toEqual([])
})

test('info panel: EXIF shown, GPS never sent until the toggle is on', async ({ page }) => {
  await openGallery(page)
  await page.getByRole('row', { name: /a-kamera\.jpg/ }).dblclick()
  await expect(overlay(page)).toBeVisible()
  const infoRes = page.waitForResponse((r) => r.url().includes('/api/v1/files/media-info'))
  await page.keyboard.press('i')
  const body = (await (await infoRes).json()) as { gps?: unknown; exif?: { hasGps?: boolean } }
  expect(body.gps).toBeUndefined()
  expect(body.exif?.hasGps).toBe(true)
  const info = page.getByTestId('viewer-info')
  await expect(info).toBeVisible()
  await expect(info).toContainText('Canon EOS R6')
  await expect(info).toContainText('3000 × 2000')
  await expect(page.getByTestId('viewer-gps-hidden')).toBeVisible()
  await expect(page.getByTestId('viewer-gps-values')).toHaveCount(0)
  const gpsRes = page.waitForResponse(
    (r) => r.url().includes('/api/v1/files/media-info') && r.url().includes('include_gps=1'),
  )
  await page.getByTestId('viewer-gps-toggle').click()
  await gpsRes
  await expect(page.getByTestId('viewer-gps-values')).toContainText('-8.22')
  await expect(page.getByTestId('viewer-gps-values')).toContainText('114.375')
  // "Buka lokasi file" closes the viewer and selects the file
  await page.getByTestId('viewer-open-location').click()
  await expect(overlay(page)).toBeHidden()
  await expect(page.getByRole('row', { name: /a-kamera\.jpg/ })).toHaveAttribute(
    'aria-selected',
    'true',
  )
})

test('svg is rasterised server-side: never requested inline, script never runs', async ({
  page,
}) => {
  await openGallery(page)
  const rawRequests: string[] = []
  page.on('request', (r) => {
    if (r.url().includes('/api/v1/files/raw')) rawRequests.push(r.url())
  })
  await page.getByRole('row', { name: /d-logo\.svg/ }).dblclick()
  await expect(overlay(page)).toBeVisible()
  const img = page.getByTestId('viewer-image')
  await expect(img).toBeVisible()
  await expect(img).toHaveAttribute('src', /variant=preview/)
  // even at 1:1 the raster preview stays (no original fetch for svg)
  await page.getByTestId('viewer-original').click()
  await page.waitForTimeout(500)
  expect(rawRequests.filter((u) => !u.includes('variant=preview'))).toEqual([])
  const res = await page.request.get(rawRequests[0]!)
  expect(res.headers()['content-type']).toBe('image/webp')
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined()
  await page.keyboard.press('Escape')
})

test('deep link ?view= opens the overlay; the preview panel offers "Buka"; axe clean', async ({
  page,
}) => {
  await page.goto(`/files${DIR}?view=${encodeURIComponent(`${DIR}/b-foto.png`)}`)
  await expect(overlay(page)).toBeVisible()
  await expect(viewerName(page)).toHaveText('b-foto.png')
  await expect(page.getByTestId('viewer-image')).toBeVisible()
  await page.keyboard.press('i')
  await expect(page.getByTestId('viewer-info')).toBeVisible()
  const results = await new AxeBuilder({ page }).include('[data-testid="viewer-overlay"]').analyze()
  const bad = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  expect(bad.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual(
    [],
  )
  await page.getByTestId('viewer-close').click()
  await expect(overlay(page)).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`/files${DIR}$`))

  // preview panel: thumbnail + "Buka" for a viewable file
  await page.getByRole('row', { name: /a-kamera\.jpg/ }).click()
  await page.keyboard.press('Alt+p')
  const panel = page.getByTestId('preview-panel')
  await expect(panel).toBeVisible()
  await page.getByTestId('preview-open').click()
  await expect(overlay(page)).toBeVisible()
  await expect(viewerName(page)).toHaveText('a-kamera.jpg')
  await page.keyboard.press('Escape')
  await expect(overlay(page)).toBeHidden()

  // a text file opens in the read-only text viewer (Step 17b); "Edit" → editor
  await page.getByRole('row', { name: /e-catatan\.txt/ }).dblclick()
  await expect(overlay(page).getByTestId('code-viewer')).toContainText('bukan gambar')
  await page.getByTestId('viewer-edit').click()
  await expect(page.getByTestId('editor-modal')).toBeVisible()
  await expect(overlay(page)).toHaveCount(0)
  expect(cspErrors).toEqual([])
})

test('Quick Look (Space) on an image is the 80 % overlay; Space closes it', async ({ page }) => {
  await openGallery(page)
  await page.getByRole('row', { name: /b-foto\.png/ }).click()
  await page.keyboard.press('Space')
  const ql = page.getByTestId('quick-look')
  await expect(ql).toBeVisible()
  await expect(ql.getByTestId('viewer-image')).toBeVisible()
  await expect(ql.getByTestId('viewer-position')).toHaveCount(0)
  await expect(page).not.toHaveURL(/view=/)
  await page.keyboard.press('Space')
  await expect(ql).toBeHidden()
})

// ---------------------------------------------------------------------------
// PDF viewer (pdf.js): streaming, sidebar, search, password, virtualisation
// ---------------------------------------------------------------------------

const PDF_DIR = '/galeri/pdf'
const BIG_BYTES = 100 * 1024 * 1024
let pdfSeeded = false
function seedPdf(dir: string) {
  if (pdfSeeded) return
  pdfSeeded = true
  mkdirSync(dir, { recursive: true })
  writeFileSync(
    join(dir, 'dokumen-20.pdf'),
    makePdf({
      pages: 20,
      text: (n) => (n === 7 ? `Halaman ${n} kata-unik-xyz` : `Halaman ${n}`),
      outline: [
        { title: 'Bab satu', page: 1 },
        { title: 'Bab dua', page: 12 },
        { title: 'Lampiran', page: 20 },
      ],
    }),
  )
  writeFileSync(join(dir, 'rahasia.pdf'), makePdf({ pages: 3, password: 'pdf123' }))
  writeFileSync(join(dir, 'besar.pdf'), makePdf({ pages: 3, junkBytes: BIG_BYTES }))
  writeFileSync(join(dir, 'banyak.pdf'), makePdf({ pages: 500 }))
}

async function openPdfFolder(page: Page) {
  if (!pdfSeeded) {
    await apiMkdir(page.request, '/galeri', 'pdf')
    seedPdf(join(e2eStorageRoot(), 'admin', 'galeri', 'pdf'))
  }
  await page.goto(`/files${PDF_DIR}`)
  await expect(page.getByRole('row', { name: /dokumen-20\.pdf/ })).toBeVisible()
}

const firstCanvas = (page: Page) =>
  page.locator('[data-testid="pdf-container"] .page[data-page-number="1"] canvas')

test('pdf: self-hosted worker, sidebar thumbnails/outline, text search jumps to the page', async ({
  page,
}) => {
  await openPdfFolder(page)
  const workerReq = page.waitForRequest((r) => /pdf\.worker\.min-.*\.mjs$/.test(r.url()))
  await page.getByRole('row', { name: /dokumen-20\.pdf/ }).dblclick()
  await expect(overlay(page)).toHaveAttribute('data-kind', 'pdf')
  const worker = await workerReq
  expect(new URL(worker.url()).origin).toBe(new URL(page.url()).origin)
  await expect(firstCanvas(page)).toBeVisible({ timeout: 20_000 })
  await expect(page.getByTestId('pdf-page-count')).toHaveText('dari 20')
  await expect(page.getByTestId('pdf-page-input')).toHaveValue('1')

  // sidebar: 20 thumbnails, clicking one navigates
  await page.getByTestId('pdf-sidebar-toggle').click()
  await expect(page.getByTestId('pdf-thumb')).toHaveCount(20)
  await expect(page.getByTestId('pdf-thumb').first().locator('img')).toBeVisible({
    timeout: 15_000,
  })
  await page.getByTestId('pdf-thumb').nth(4).click()
  await expect(page.getByTestId('pdf-page-input')).toHaveValue('5')
  // outline
  await page.getByTestId('pdf-tab-outline').click()
  await expect(page.getByTestId('pdf-outline-item')).toHaveCount(3)
  await page.getByTestId('pdf-outline-item').nth(1).click()
  await expect(page.getByTestId('pdf-page-input')).toHaveValue('12')

  // search: Ctrl+F, the unique word sits on page 7
  await page.keyboard.press('Control+f')
  const input = page.getByTestId('pdf-search-input')
  await expect(input).toBeFocused()
  await input.fill('kata-unik-xyz')
  await expect(page.getByTestId('pdf-search-count')).toHaveText('1 dari 1', { timeout: 15_000 })
  await expect(page.getByTestId('pdf-page-input')).toHaveValue('7')
  await expect(page.locator('.textLayer .highlight').first()).toBeVisible()
  await input.fill('tidak-ada-di-dokumen')
  await expect(page.getByTestId('pdf-search-count')).toHaveText('Tidak ditemukan', {
    timeout: 15_000,
  })
  await page.getByTestId('pdf-search-input').press('Escape')
  await expect(page.getByTestId('pdf-search')).toHaveCount(0)

  // page input, zoom preset and rotation go through pdf.js (page-width keeps
  // the width, so rotating an A4 portrait page changes its height)
  await page.getByTestId('pdf-page-input').fill('20')
  await page.getByTestId('pdf-page-input').press('Enter')
  await expect(page.getByTestId('pdf-page-input')).toHaveValue('20')
  await page.getByTestId('pdf-zoom').click()
  await page.getByRole('option', { name: 'Lebar halaman' }).click()
  await expect(page.getByTestId('pdf-zoom')).toContainText('Lebar halaman')
  const before = await page
    .locator('[data-testid="pdf-container"] .page')
    .first()
    .evaluate((el) => (el as HTMLElement).offsetHeight)
  await page.getByTestId('pdf-rotate').click()
  await expect
    .poll(() =>
      page
        .locator('[data-testid="pdf-container"] .page')
        .first()
        .evaluate((el) => (el as HTMLElement).offsetHeight),
    )
    .not.toBe(before)
  // arrows still move between files of the same kind (pdf → pdf)
  await page.keyboard.press('Escape')
  await expect(overlay(page)).toBeHidden()
  expect(cspErrors).toEqual([])
})

test('pdf: password prompt, wrong then right', async ({ page }) => {
  await openPdfFolder(page)
  await page.getByRole('row', { name: /rahasia\.pdf/ }).dblclick()
  const form = page.getByTestId('pdf-password')
  await expect(form).toBeVisible({ timeout: 20_000 })
  await page.getByTestId('pdf-password-input').fill('salah')
  await page.getByTestId('pdf-password-open').click()
  await expect(page.getByTestId('pdf-password-wrong')).toBeVisible({ timeout: 15_000 })
  await page.getByTestId('pdf-password-input').fill('pdf123')
  await page.getByTestId('pdf-password-open').click()
  await expect(firstCanvas(page)).toBeVisible({ timeout: 20_000 })
  await expect(page.getByTestId('pdf-page-count')).toHaveText('dari 3')
  await page.keyboard.press('Escape')
})

test('pdf: a 100 MB file shows page 1 through Range requests long before it is downloaded', async ({
  page,
}) => {
  await openPdfFolder(page)
  let ranged = 0
  let bytes = 0
  page.on('response', (r) => {
    if (!r.url().includes('/api/v1/files/raw') || !r.url().includes('besar.pdf')) return
    if (r.request().headers()['range']) ranged++
    const len = Number(r.headers()['content-length'] ?? 0)
    if (r.status() === 206) bytes += len
  })
  await page.getByRole('row', { name: /besar\.pdf/ }).dblclick()
  await expect(firstCanvas(page)).toBeVisible({ timeout: 30_000 })
  await expect(page.getByTestId('pdf-page-count')).toHaveText('dari 3')
  expect(ranged).toBeGreaterThan(0)
  // only a few 64 KB chunks were needed for the first page, not the 100 MB body
  expect(bytes).toBeLessThan(8 * 1024 * 1024)
  await page.keyboard.press('Escape')
})

test('pdf: 500 pages stay virtualised while scrolling to the end', async ({ page }) => {
  await openPdfFolder(page)
  await page.getByRole('row', { name: /banyak\.pdf/ }).dblclick()
  await expect(firstCanvas(page)).toBeVisible({ timeout: 30_000 })
  await expect(page.getByTestId('pdf-page-count')).toHaveText('dari 500')
  const container = page.getByTestId('pdf-container')
  // scroll through the document in big steps, then to the very end
  for (let i = 1; i <= 10; i++) {
    await container.evaluate((el, k) => {
      el.scrollTop = (el.scrollHeight * k) / 10
    }, i)
    await page.waitForTimeout(150)
  }
  await expect(page.getByTestId('pdf-page-input')).toHaveValue('500', { timeout: 15_000 })
  // pdf.js keeps only the visible pages (+ a small buffer) rendered
  const canvases = await page.locator('[data-testid="pdf-container"] .page canvas').count()
  expect(canvases).toBeLessThanOrEqual(12)
  await page.keyboard.press('Escape')
  expect(cspErrors).toEqual([])
})
