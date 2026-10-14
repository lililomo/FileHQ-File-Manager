import { expect, test, type Page } from '@playwright/test'
import { createHash, randomBytes } from 'node:crypto'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { apiMkdir, apiResetPrefs, csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

/** Step 12a AC through the real binary (UPLOAD_CHUNK_SIZE=1MB in backend.mjs):
 *  pause/resume/checksum, network abort → automatic retry, reload → resume
 *  from the tus fingerprint, folder upload with structure, conflict rename,
 *  quota refused before anything starts, paste from the clipboard. */
test.describe.configure({ mode: 'serial', timeout: 180_000 })

const DIR = '/up'
const ORIGIN = 'http://127.0.0.1:18090'
const MB = 1024 * 1024
let seeded = false
let work = ''

function makeFile(name: string, bytes: number): { path: string; sha256: string } {
  const buf = randomBytes(bytes)
  const path = join(work, name)
  writeFileSync(path, buf)
  return { path, sha256: createHash('sha256').update(buf).digest('hex') }
}

async function apiList(page: Page, path: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(path)}`)
  if (res.status() === 404) return []
  expect(res.status()).toBe(200)
  return ((await res.json()) as { items: { name: string }[] }).items.map((i) => i.name)
}

async function apiSha256(page: Page, path: string): Promise<string> {
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/files/checksum', {
    data: { path, algos: ['sha256'] },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
  return ((await res.json()) as { sha256: string }).sha256
}

function setAdminQuota(bytes: number): void {
  const db = new DatabaseSync(join(e2eStorageRoot(), '.system', 'db', 'app.db'))
  try {
    db.prepare(`UPDATE users SET quota_bytes = ? WHERE username = 'admin'`).run(bytes)
  } finally {
    db.close()
  }
}

const rowByName = (page: Page, name: string) =>
  page.locator(`[data-testid="transfer-row"][data-name="${name}"]`)
const fileRow = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)

/** Pick files through the toolbar menu (real file chooser, intercepted). */
async function pickFiles(page: Page, kind: 'file' | 'folder', paths: string | string[]) {
  await page.getByTestId('toolbar-upload').click()
  const [chooser] = await Promise.all([
    page.waitForEvent('filechooser'),
    page.getByTestId(`toolbar-upload-${kind}`).click(),
  ])
  await chooser.setFiles(paths)
}

/** Slows every PATCH so a transfer stays observable for a while. */
async function slowPatches(page: Page, ms: number) {
  await page.route('**/api/v1/upload/tus/**', async (route) => {
    if (route.request().method() === 'PATCH') await new Promise((r) => setTimeout(r, ms))
    await route.continue()
  })
}

test.beforeAll(() => {
  work = mkdtempSync(join(tmpdir(), 'fm-e2e-upload-'))
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
    await apiMkdir(page.request, '/', 'up')
  }
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('toolbar-upload')).toBeVisible()
})

test('50 MB upload: pause, resume from the server offset, identical checksum', async ({ page }) => {
  const big = makeFile('big.bin', 50 * MB)
  await slowPatches(page, 120)
  await pickFiles(page, 'file', big.path)
  const row = rowByName(page, 'big.bin')
  await expect(row).toHaveAttribute('data-status', 'uploading')
  await expect(page.getByTestId('transfer-active-count')).toBeVisible()
  // let a few chunks through, then pause
  await page.waitForTimeout(1500)
  await row.getByTestId('transfer-pause').click()
  await expect(row).toHaveAttribute('data-status', 'paused')
  const pausedPercent = await row.getByTestId('transfer-progress').getAttribute('aria-label')
  await page.waitForTimeout(800)
  expect(await row.getByTestId('transfer-progress').getAttribute('aria-label')).toBe(pausedPercent)
  await page.unroute('**/api/v1/upload/tus/**')
  await row.getByTestId('transfer-resume').click()
  await expect(row).toHaveAttribute('data-status', 'uploading')
  // tus continued from the server offset instead of starting over
  await expect(row).toHaveAttribute('data-resumed', 'true')
  await expect(row).toHaveAttribute('data-status', 'done', { timeout: 120_000 })
  await expect(page.locator('[data-sonner-toaster]').getByText(/1 file diunggah/)).toBeVisible()
  await expect(fileRow(page, `${DIR}/big.bin`)).toBeVisible()
  expect(await apiSha256(page, `${DIR}/big.bin`)).toBe(big.sha256)
})

test('a dropped connection is retried automatically without a second upload session', async ({
  page,
}) => {
  const f = makeFile('flaky.bin', 6 * MB)
  let aborted = 0
  let posts = 0
  page.on('request', (req) => {
    if (req.method() === 'POST' && req.url().includes('/api/v1/upload/tus')) posts++
  })
  await page.route('**/api/v1/upload/tus/**', async (route) => {
    if (route.request().method() === 'PATCH' && aborted < 2) {
      aborted++
      await route.abort('connectionreset')
      return
    }
    await route.continue()
  })
  await pickFiles(page, 'file', f.path)
  const row = rowByName(page, 'flaky.bin')
  await expect(row).toHaveAttribute('data-status', 'done', { timeout: 60_000 })
  expect(aborted).toBe(2)
  expect(posts).toBe(1)
  expect(await apiSha256(page, `${DIR}/flaky.bin`)).toBe(f.sha256)
})

test('reload in the middle of an upload: the panel offers to resume and tus continues from the fingerprint', async ({
  page,
}) => {
  const f = makeFile('reload.bin', 12 * MB)
  await slowPatches(page, 150)
  await pickFiles(page, 'file', f.path)
  const row = rowByName(page, 'reload.bin')
  await expect(row).toHaveAttribute('data-status', 'uploading')
  await page.waitForTimeout(1200)
  await page.reload()
  await expect(page.getByTestId('toolbar-upload')).toBeVisible()
  // the leftover shows up paused and detached, with the resume prompt
  const left = rowByName(page, 'reload.bin')
  await expect(left).toHaveAttribute('data-status', 'paused')
  await expect(left.getByTestId('transfer-pick-again')).toBeVisible()
  await expect(page.locator('[data-sonner-toaster]').getByText(/belum selesai/)).toBeVisible()
  let posts = 0
  page.on('request', (req) => {
    if (req.method() === 'POST' && req.url().includes('/api/v1/upload/tus')) posts++
  })
  const [chooser] = await Promise.all([
    page.waitForEvent('filechooser'),
    left.getByTestId('transfer-pick-again').click(),
  ])
  await chooser.setFiles(f.path)
  await expect(left).toHaveAttribute('data-status', 'uploading')
  await expect(left).toHaveAttribute('data-resumed', 'true')
  await expect(left).toHaveAttribute('data-status', 'done', { timeout: 60_000 })
  expect(posts).toBe(0) // HEAD + PATCH only: no new session was created
  expect(await apiSha256(page, `${DIR}/reload.bin`)).toBe(f.sha256)
})

test('folder upload keeps the structure a/b/c/file.txt', async ({ page }) => {
  const root = join(work, 'lv1')
  mkdirSync(join(root, 'lv2', 'lv3'), { recursive: true })
  writeFileSync(join(root, 'lv2', 'lv3', 'file.txt'), 'deep')
  writeFileSync(join(root, 'top.txt'), 'top')
  await pickFiles(page, 'folder', root)
  await expect(rowByName(page, 'file.txt')).toHaveAttribute('data-status', 'done', {
    timeout: 30_000,
  })
  await expect(rowByName(page, 'top.txt')).toHaveAttribute('data-status', 'done')
  expect(await apiList(page, `${DIR}/lv1/lv2/lv3`)).toEqual(['file.txt'])
  expect(await apiList(page, `${DIR}/lv1`)).toEqual(['lv2', 'top.txt'])
  await expect(fileRow(page, `${DIR}/lv1`)).toBeVisible()
})

test('uploading an existing name opens the conflict dialog; "Ganti nama" yields dup (1).txt', async ({
  page,
}) => {
  const f = makeFile('dup.txt', 2048)
  await pickFiles(page, 'file', f.path)
  await expect(rowByName(page, 'dup.txt').first()).toHaveAttribute('data-status', 'done', {
    timeout: 30_000,
  })
  await pickFiles(page, 'file', f.path)
  const dialog = page.getByTestId('conflict-dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('dup.txt')
  await page.getByTestId('conflict-rename').click()
  const second = rowByName(page, 'dup.txt').nth(1)
  await expect(second).toHaveAttribute('data-status', 'done', { timeout: 30_000 })
  await expect(second.getByTestId('transfer-meta')).toContainText('dup (1).txt')
  await expect(fileRow(page, `${DIR}/dup (1).txt`)).toBeVisible()
})

test('not enough quota is refused before any tus request', async ({ page }) => {
  setAdminQuota(1 * MB)
  try {
    const f = makeFile('toobig.bin', 2 * MB)
    let tusRequests = 0
    page.on('request', (req) => {
      if (req.url().includes('/api/v1/upload/tus')) tusRequests++
    })
    await pickFiles(page, 'file', f.path)
    const dialog = page.getByTestId('upload-precheck')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('Kuota tidak cukup')
    await expect(dialog).toContainText('2 MB')
    await expect(page.getByTestId('upload-precheck-continue')).toHaveCount(0)
    await page.getByTestId('upload-precheck-cancel').click()
    await expect(dialog).toBeHidden()
    expect(tusRequests).toBe(0)
    expect(await apiList(page, DIR)).not.toContain('toobig.bin')
  } finally {
    setAdminQuota(10 * 1024 * MB)
  }
})

test('pasting an image from the clipboard creates image-YYYYMMDD-HHMMSS.png in the open folder', async ({
  page,
}) => {
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('Escape')
  await page.evaluate(() => {
    const png = new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82])
    const dt = new DataTransfer()
    dt.items.add(new File([png], 'image.png', { type: 'image/png' }))
    document.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true }))
  })
  const row = page.locator('[data-testid="transfer-row"][data-name^="image-"]')
  await expect(row).toHaveAttribute('data-status', 'done', { timeout: 30_000 })
  const name = await row.getAttribute('data-name')
  expect(name).toMatch(/^image-\d{8}-\d{6}\.png$/)
  expect(await apiList(page, DIR)).toContain(name)
})
