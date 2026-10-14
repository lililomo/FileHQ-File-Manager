import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { apiMkdir, apiResetPrefs, csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

test.describe.configure({ mode: 'serial', timeout: 240_000 })

const DIR = '/media'
const FFMPEG = '/usr/bin/ffmpeg'
const VIPS = '/usr/bin/vips'
const MINIMAL_PDF =
  '%PDF-1.1\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]>>endobj\ntrailer<</Root 1 0 R>>'
const SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" width="120" height="60"><rect width="120" height="60" fill="#2563eb"/></svg>'

/** Media fixtures are generated at test time with the same tools the server
 *  uses (nothing binary is committed). */
function seedMedia(dir: string) {
  mkdirSync(dir, { recursive: true })
  execFileSync(VIPS, ['black', join(dir, 'foto.png'), '96', '64'])
  execFileSync(VIPS, ['black', join(dir, 'gambar.jpg'), '64', '64'])
  execFileSync(VIPS, ['black', join(dir, 'web.webp'), '64', '64'])
  execFileSync(VIPS, ['black', join(dir, 'anim.gif'), '32', '32'])
  writeFileSync(join(dir, 'logo.svg'), SVG)
  writeFileSync(join(dir, 'dokumen.pdf'), MINIMAL_PDF)
  writeFileSync(join(dir, 'catatan.txt'), 'teks biasa')
  execFileSync(FFMPEG, [
    '-nostdin',
    '-v',
    'error',
    '-y',
    '-f',
    'lavfi',
    '-i',
    'testsrc=size=160x120:rate=10',
    '-t',
    '2',
    '-c:v',
    'libx264',
    '-pix_fmt',
    'yuv420p',
    join(dir, 'film.mp4'),
  ])
  execFileSync(FFMPEG, [
    '-nostdin',
    '-v',
    'error',
    '-y',
    '-f',
    'lavfi',
    '-i',
    'sine=frequency=440:duration=1',
    '-i',
    join(dir, 'foto.png'),
    '-map',
    '0:a',
    '-map',
    '1:v',
    '-c:a',
    'libmp3lame',
    '-c:v',
    'mjpeg',
    '-id3v2_version',
    '3',
    '-metadata',
    'title=Lagu Uji',
    '-disposition:v',
    'attached_pic',
    join(dir, 'lagu.mp3'),
  ])
}

const THUMB_FILES = [
  'foto.png',
  'gambar.jpg',
  'web.webp',
  'anim.gif',
  'logo.svg',
  'dokumen.pdf',
  'film.mp4',
  'lagu.mp3',
]

let seeded = false
test.beforeEach(async ({ page }) => {
  test.skip(!existsSync(FFMPEG) || !existsSync(VIPS), 'ffmpeg/vips not installed')
  await loginAsAdmin(page)
  await page.waitForTimeout(700)
  await apiResetPrefs(page.request)
  await setPrefs(page, { 'view.mode': null, 'view.remember_per_folder': null })
  await deleteFolderPref(page, DIR)
  await deleteFolderPref(page, '/')
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'media')
    seedMedia(join(e2eStorageRoot(), 'admin', 'media'))
  }
})

async function setPrefs(page: Page, prefs: Record<string, unknown>) {
  const token = await csrfToken(page.request)
  const res = await page.request.put('/api/v1/me/preferences', {
    data: { prefs },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
  expect(res.status(), await res.text()).toBe(200)
}

async function deleteFolderPref(page: Page, path: string) {
  const token = await csrfToken(page.request)
  await page.request.delete(`/api/v1/me/folder-prefs?path=${encodeURIComponent(path)}`, {
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
}

async function openMedia(page: Page) {
  await page.goto('/files/media')
  await expect(page.getByTestId('browser')).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText('9 item')
}

test('five views: toolbar menu and Ctrl+1…5 switch the view, thumbnails render in icon views', async ({
  page,
}) => {
  await openMedia(page)
  await expect(page.getByTestId('browser')).toHaveAttribute('data-view', 'details')
  await expect(page.getByTestId('details-grid')).toBeVisible()

  await page.getByTestId('toolbar-view').click()
  await page.getByTestId('view-mode-large').click()
  await expect(page.getByTestId('browser')).toHaveAttribute('data-view', 'large')
  const grid = page.getByTestId('large-grid')
  await expect(grid).toBeVisible()
  await expect(grid.locator('[data-cell]')).toHaveCount(9)

  // every supported type gets a real thumbnail (svg rasterised, pdf page 1, video frame, audio cover)
  for (const name of THUMB_FILES) {
    const img = grid.locator(`[data-path="${DIR}/${name}"] [data-testid="thumb-img"]`)
    await expect(img, name).toBeVisible({ timeout: 90_000 })
    const size = await img.evaluate((el) => (el as HTMLImageElement).naturalWidth)
    expect(size, name).toBeGreaterThan(0)
    const src = await img.getAttribute('src')
    expect(src).toContain('/api/v1/files/thumb?')
    expect(src).toContain('sig=')
  }
  // the text file keeps its type icon, never requests a thumbnail
  const txt = grid.locator(`[data-path="${DIR}/catatan.txt"]`)
  await expect(txt.locator('[data-testid="thumb-img"]')).toHaveCount(0)
  await expect(txt.locator('[data-thumb="idle"]')).toHaveCount(1)
  expect(await txt.locator('svg').count()).toBeGreaterThan(0)

  // keyboard: Ctrl+1…5
  await grid.focus()
  for (const [key, view] of [
    ['Control+2', 'list'],
    ['Control+3', 'tiles'],
    ['Control+5', 'xlarge'],
    ['Control+4', 'large'],
    ['Control+1', 'details'],
  ] as const) {
    await page.keyboard.press(key)
    await expect(page.getByTestId('browser')).toHaveAttribute('data-view', view)
  }
  // list view flows in columns; tiles and xlarge exist
  await page.keyboard.press('Control+2')
  await expect(page.getByTestId('list-grid')).toBeVisible()
  await expect(page.getByTestId('list-grid').locator('[data-cell]')).toHaveCount(9)
  await page.keyboard.press('Control+3')
  await expect(page.getByTestId('tiles-grid').locator('[data-cell]')).toHaveCount(9)
  await page.keyboard.press('Control+5')
  await expect(page.getByTestId('xlarge-grid')).toBeVisible()
})

test('view is remembered per folder and the global default follows the last change', async ({
  page,
}) => {
  await openMedia(page)
  await page.getByTestId('toolbar-view').click()
  await page.getByTestId('view-mode-tiles').click()
  await expect(page.getByTestId('browser')).toHaveAttribute('data-view', 'tiles')
  await page.waitForTimeout(800) // debounced preference write
  await page.reload()
  await expect(page.getByTestId('browser')).toHaveAttribute('data-view', 'tiles')

  // root inherits the new global default; switching it there must not change /media
  await page.goto('/files')
  await expect(page.getByTestId('browser')).toHaveAttribute('data-view', 'tiles')
  await page.getByTestId('toolbar-view').click()
  await page.getByTestId('view-mode-details').click()
  await expect(page.getByTestId('browser')).toHaveAttribute('data-view', 'details')
  await page.waitForTimeout(800)
  await page.goto('/files/media')
  await expect(page.getByTestId('browser')).toHaveAttribute('data-view', 'tiles')

  // with "remember per folder" off, the folder preference is ignored
  await page.getByTestId('toolbar-view').click()
  await page.getByTestId('view-remember-folder').click()
  await page.keyboard.press('Escape')
  await page.waitForTimeout(800)
  await page.goto('/files/media')
  await expect(page.getByTestId('browser')).toHaveAttribute('data-view', 'details')
  await deleteFolderPref(page, DIR)
  await deleteFolderPref(page, '/')
})

test('marquee selects intersecting cells; Ctrl+A and Escape work in grid views', async ({
  page,
}) => {
  await page.setViewportSize({ width: 900, height: 700 })
  await openMedia(page)
  await page.keyboard.press('Control+4')
  const grid = page.getByTestId('large-grid')
  await expect(grid).toBeVisible()
  const cells = grid.locator('[data-cell]')
  await expect(cells).toHaveCount(9)
  // Layout: C columns of 9 cells; the last row has r cells, so (row rows-1, col r)
  // is an empty slot. Widen the viewport until such a slot exists.
  let C = 0
  let r = 0
  for (let width = 900; width <= 1400; width += 160) {
    await page.setViewportSize({ width, height: 700 })
    await expect
      .poll(async () => Number(await grid.getAttribute('data-columns')))
      .toBeGreaterThan(0)
    C = Number(await grid.getAttribute('data-columns'))
    const rows = Math.ceil(9 / C)
    r = 9 - (rows - 1) * C
    if (r < C) break
  }
  expect(r).toBeLessThan(C)
  const last = await cells.nth(8).boundingBox()
  const target = await cells.nth(r - 1).boundingBox() // row 0, same column as the last cell
  if (!last || !target) throw new Error('layout')
  const startX = last.x + last.width + 8 + 12 // empty slot right of the last cell
  const startY = last.y + 12
  await page.mouse.move(startX, startY)
  await page.mouse.down()
  await page.mouse.move(startX - 20, startY - 20)
  await page.mouse.move(target.x + target.width / 2, target.y + target.height / 2, { steps: 8 })
  await expect(page.getByTestId('marquee')).toBeVisible()
  await page.mouse.up()
  const expected = [...Array(9).keys()].filter((i) => i % C === r - 1 || i % C === r)
  await expect(page.getByTestId('status-selected')).toContainText(`${expected.length} dipilih`)
  for (let i = 0; i < 9; i++)
    await expect(cells.nth(i)).toHaveAttribute('aria-selected', String(expected.includes(i)))

  // click on background clears, Ctrl+A selects all, Escape clears
  await page.mouse.click(startX, startY)
  await expect(page.getByTestId('status-selected')).toHaveCount(0)
  await grid.focus()
  await page.keyboard.press('Control+a')
  await expect(page.getByTestId('status-selected')).toContainText('9 dipilih')
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('status-selected')).toHaveCount(0)

  // keyboard: arrows move in two dimensions, Space = Quick Look
  await cells.nth(0).click()
  await page.keyboard.press('ArrowRight')
  await expect(cells.nth(1)).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('ArrowDown')
  await expect(cells.nth(Math.min(8, 1 + C))).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('ArrowUp')
  await expect(cells.nth(1 + C <= 8 ? 1 : Math.min(8, 1 + C))).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await page.keyboard.press(' ')
  await expect(page.getByTestId('quick-look')).toBeVisible()
  await page.keyboard.press('Escape')
})

test('Dimensi/Durasi columns are filled from media metadata and sort puts unknown last', async ({
  page,
}) => {
  await setPrefs(page, {
    'view.columns': { visible: ['name', 'size', 'dimensions', 'duration'], order: [], widths: {} },
  })
  await openMedia(page)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  const row = (name: string) =>
    page.locator(`[data-testid="details-body"] [data-path="${DIR}/${name}"]`)
  await expect(row('foto.png')).toContainText('96 × 64', { timeout: 30_000 })
  await expect(row('film.mp4')).toContainText('160 × 120')
  await expect(row('film.mp4')).toContainText('0:02')
  await expect(row('lagu.mp3')).toContainText('0:01')
  await expect(row('catatan.txt')).toContainText('—')

  await page.getByTestId('toolbar-sort').click()
  await page.getByTestId('sort-field-duration').click()
  await page.keyboard.press('Escape')
  const names = () =>
    page
      .locator('[data-testid="details-body"] [role="row"]')
      .evaluateAll((rows) =>
        rows.map((r) => (r as HTMLElement).dataset.path?.split('/').pop() ?? ''),
      )
  await expect.poll(names).toEqual(expect.arrayContaining(['lagu.mp3', 'film.mp4']))
  const order = await names()
  expect(order.indexOf('lagu.mp3')).toBeLessThan(order.indexOf('film.mp4'))
  expect(order.indexOf('film.mp4')).toBeLessThan(order.indexOf('catatan.txt')) // unknown last
  await setPrefs(page, { 'view.columns': null, 'view.sort': null })
  await deleteFolderPref(page, DIR)
})
