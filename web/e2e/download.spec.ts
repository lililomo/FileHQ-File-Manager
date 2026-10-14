import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { apiCreateFile, apiMkdir, apiResetPrefs, csrfToken, loginAsAdmin } from './fixtures'

/** Step 12b AC through the real binary: single-file download via a signed URL
 *  (content identical, attachment disposition), HTTP Range → 206, streamed zip
 *  of two files + a folder verified with `unzip -t`, HTML served as an
 *  octet-stream attachment, tampered signature → 403 SIGNATURE_INVALID.
 *  Expired signatures and disabled accounts need a fake clock / admin API and
 *  are covered by httpapi/download_test.go. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/dl'
const ORIGIN = 'http://127.0.0.1:18090'
let seeded = false
let work = ''

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)

async function sign(page: Page, path: string, purpose: 'download' | 'media' | 'inline') {
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/files/sign', {
    data: { path, purpose },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
  return ((await res.json()) as { url: string }).url
}

test.beforeAll(() => {
  work = mkdtempSync(join(tmpdir(), 'fm-e2e-download-'))
})
test.afterAll(() => {
  rmSync(work, { recursive: true, force: true })
})

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'dl')
    await apiCreateFile(page.request, DIR, 'laporan – résumé.txt', 'isi laporan '.repeat(200))
    await apiCreateFile(page.request, DIR, 'kedua.txt', 'kedua')
    await apiCreateFile(page.request, DIR, 'file.html', '<html><script>alert(1)</script></html>')
    await apiMkdir(page.request, DIR, 'proyek')
    await apiCreateFile(page.request, `${DIR}/proyek`, 'a.txt', 'A')
    await apiMkdir(page.request, `${DIR}/proyek`, 'sub')
    await apiCreateFile(page.request, `${DIR}/proyek/sub`, 'b.txt', 'BB')
  }
  await page.goto(`/files${DIR}`)
  await expect(row(page, `${DIR}/kedua.txt`)).toBeVisible()
})

test('download a file from the context menu: identical content, attachment name', async ({
  page,
}) => {
  await row(page, `${DIR}/laporan – résumé.txt`).click({ button: 'right' })
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByTestId('cm-download').click(),
  ])
  expect(download.suggestedFilename()).toBe('laporan – résumé.txt')
  const saved = join(work, 'laporan.txt')
  await download.saveAs(saved)
  expect(readFileSync(saved, 'utf8')).toBe('isi laporan '.repeat(200))
  // the live region announced it
  await expect(page.locator('#live-polite')).toContainText('Mengunduh laporan – résumé.txt')
})

test('signed URL: headers, Range → 206, HTML as octet-stream attachment, tamper → 403', async ({
  page,
}) => {
  const url = await sign(page, `${DIR}/laporan – résumé.txt`, 'download')
  expect(url).toMatch(/^\/api\/v1\/files\/download\?/)
  // a fresh context without cookies: the signature alone authenticates
  const anon = await page.context().browser()!.newContext()
  try {
    const head = await anon.request.head(ORIGIN + url)
    expect(head.status()).toBe(200)
    const h = head.headers()
    expect(h['accept-ranges']).toBe('bytes')
    expect(h['etag']).toBeTruthy()
    expect(h['content-disposition']).toMatch(
      /^attachment; filename="laporan _ r_sum_.txt"; filename\*=UTF-8''/,
    )
    expect(h['content-security-policy']).toBe("sandbox; default-src 'none'")
    expect(h['x-content-type-options']).toBe('nosniff')
    expect(h['x-accel-buffering']).toBe('no')

    const part = await anon.request.get(ORIGIN + url, { headers: { Range: 'bytes=100-199' } })
    expect(part.status()).toBe(206)
    expect(part.headers()['content-range']).toBe('bytes 100-199/2400')
    expect((await part.body()).length).toBe(100)

    const raw = await sign(page, `${DIR}/file.html`, 'media')
    const html = await anon.request.get(ORIGIN + raw)
    expect(html.status()).toBe(200)
    expect(html.headers()['content-type']).toBe('application/octet-stream')
    expect(html.headers()['content-disposition']).toMatch(/^attachment;/)

    const tampered = url.replace(/sig=(.)/, (_m, c: string) => `sig=${c === 'A' ? 'B' : 'A'}`)
    const bad = await anon.request.get(ORIGIN + tampered)
    expect(bad.status()).toBe(403)
    expect(((await bad.json()) as { error: { code: string } }).error.code).toBe('SIGNATURE_INVALID')
    const noSig = await anon.request.get(ORIGIN + url.replace(/&sig=[^&]+/, ''))
    expect(noSig.status()).toBe(403)
  } finally {
    await anon.close()
  }
})

test('zip of 2 files + 1 folder streams and passes unzip -t', async ({ page }) => {
  await row(page, `${DIR}/kedua.txt`).click()
  await row(page, `${DIR}/laporan – résumé.txt`).click({ modifiers: ['ControlOrMeta'] })
  await row(page, `${DIR}/proyek`).click({ modifiers: ['ControlOrMeta'] })
  await row(page, `${DIR}/proyek`).click({ button: 'right' })
  await expect(page.getByTestId('context-menu')).toHaveAttribute('data-context', 'multi')
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByTestId('cm-downloadZip').click(),
  ])
  expect(download.suggestedFilename()).toMatch(/^[a-z0-9-]+-\d{8}-\d{4}\.zip$/)
  const saved = join(work, 'pilihan.zip')
  await download.saveAs(saved)
  const listing = execFileSync('unzip', ['-t', saved], { encoding: 'utf8' })
  expect(listing).toContain('No errors detected')
  for (const name of ['kedua.txt', 'laporan – résumé.txt', 'proyek/a.txt', 'proyek/sub/b.txt'])
    expect(listing).toContain(`testing: ${name}`)
  expect(listing).not.toContain('_ERRORS.txt')
  await expect(page.locator('#live-polite')).toContainText(/Download .*\.zip dimulai/)

  // a single folder downloads as <folder>.zip from its own menu
  await row(page, `${DIR}/proyek`).click()
  await row(page, `${DIR}/proyek`).click({ button: 'right' })
  const [folderZip] = await Promise.all([
    page.waitForEvent('download'),
    page.getByTestId('cm-downloadZip').click(),
  ])
  expect(folderZip.suggestedFilename()).toBe('proyek.zip')
  const savedFolder = join(work, 'proyek.zip')
  await folderZip.saveAs(savedFolder)
  const folderListing = execFileSync('unzip', ['-t', savedFolder], { encoding: 'utf8' })
  expect(folderListing).toContain('testing: proyek/sub/b.txt')
})

test('preview panel and Properties offer Download', async ({ page }) => {
  await row(page, `${DIR}/kedua.txt`).click()
  await page.keyboard.press('Alt+p')
  const panel = page.getByTestId('preview-panel')
  await expect(panel).toBeVisible()
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    panel.getByTestId('preview-download').click(),
  ])
  expect(download.suggestedFilename()).toBe('kedua.txt')
  await page.keyboard.press('Alt+p')

  await row(page, `${DIR}/kedua.txt`).click()
  await page.keyboard.press('Alt+Enter')
  const props = page.getByTestId('properties')
  await expect(props).toBeVisible()
  const [fromProps] = await Promise.all([
    page.waitForEvent('download'),
    props.getByTestId('props-download').click(),
  ])
  expect(fromProps.suggestedFilename()).toBe('kedua.txt')
  await expect(props).toHaveCount(0)
})
