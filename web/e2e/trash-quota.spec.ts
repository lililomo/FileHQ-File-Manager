import { expect, test, type Page } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { DatabaseSync } from 'node:sqlite'
import { join } from 'node:path'
import {
  apiCreateFile,
  apiMkdir,
  apiResetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/tq'
const ORIGIN = 'http://127.0.0.1:18090'
let seeded = false

async function apiDelete(page: Page, paths: string[]): Promise<string[]> {
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/files/delete', {
    data: { paths },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
  const body = (await res.json()) as { results: { trashId: string }[] }
  return body.results.map((r) => r.trashId)
}

async function apiList(page: Page, path: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(path)}`)
  expect(res.status()).toBe(200)
  return ((await res.json()) as { items: { name: string }[] }).items.map((i) => i.name)
}

/** The admin quota is changed straight in SQLite (there is no admin API before Step 19a). */
function setAdminQuota(bytes: number) {
  const db = new DatabaseSync(join(e2eStorageRoot(), '.system', 'db', 'app.db'))
  try {
    db.exec('PRAGMA busy_timeout = 5000')
    db.prepare(`UPDATE users SET quota_bytes = ? WHERE username = 'admin'`).run(bytes)
  } finally {
    db.close()
  }
}

const toast = (page: Page, text: string | RegExp) =>
  page.locator('[data-sonner-toaster]').getByText(text)
const rowByName = (page: Page, name: string) =>
  page.locator(`[data-testid="trash-row"][data-name="${name}"]`)
const fileRow = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'tq')
    await apiMkdir(page.request, DIR, 'other')
    await apiMkdir(page.request, DIR, 'sub')
    await apiCreateFile(page.request, DIR, 'a.txt', 'aaaa')
    await apiCreateFile(page.request, DIR, 'b.txt', 'bb')
    await apiCreateFile(page.request, `${DIR}/sub`, 'c.txt', 'c')
    await apiCreateFile(page.request, `${DIR}/sub`, 'd.txt', 'dd')
    await apiCreateFile(page.request, DIR, 'e.txt', 'eeeee')
    await apiCreateFile(page.request, DIR, 'f.txt', 'f')
  }
})

test('deleted items appear in the Recycle Bin and restore to their original folder', async ({
  page,
}) => {
  await apiDelete(page, [`${DIR}/a.txt`])
  await page.goto('/trash')
  await expect(page.getByTestId('trash-page')).toBeVisible()
  await expect(page.getByTestId('trash-retention')).toContainText(/hari|otomatis/)
  const row = rowByName(page, 'a.txt')
  await expect(row).toBeVisible()
  await expect(row).toContainText('/tq')
  await expect(page.getByTestId('trash-status-items')).toContainText('1 item')
  await row.click()
  await expect(page.getByTestId('trash-status-selected')).toContainText('1 dipilih')
  await page.getByTestId('trash-restore').click()
  await expect(toast(page, '1 item dipulihkan')).toBeVisible()
  await expect(row).toHaveCount(0)
  expect(await apiList(page, DIR)).toContain('a.txt')
  // the sidebar quota comes from /me/quota
  await expect(page.getByTestId('quota-indicator')).toBeVisible()
})

test('restore to another folder via the folder picker, conflict dialog on an occupied name', async ({
  page,
}) => {
  await apiDelete(page, [`${DIR}/b.txt`])
  await page.goto('/trash')
  const row = rowByName(page, 'b.txt')
  await row.click()
  await page.getByTestId('trash-restore-to').click()
  const dialog = page.getByTestId('restore-to')
  await expect(dialog).toBeVisible()
  await dialog.getByTestId('restore-to-path').fill('/tq/other')
  await dialog.getByTestId('restore-to-submit').click()
  await expect(toast(page, '1 item dipulihkan')).toBeVisible()
  expect(await apiList(page, `${DIR}/other`)).toContain('b.txt')
  // same name again → conflict dialog → rename
  await apiCreateFile(page.request, DIR, 'b.txt', 'second')
  await apiDelete(page, [`${DIR}/b.txt`])
  await page.goto('/trash')
  await rowByName(page, 'b.txt').click()
  await page.getByTestId('trash-restore-to').click()
  await page.getByTestId('restore-to-path').fill('/tq/other')
  await page.getByTestId('restore-to-submit').click()
  const conflict = page.getByTestId('conflict-dialog')
  await expect(conflict).toBeVisible()
  await conflict.getByTestId('conflict-rename').click()
  await expect(toast(page, '1 item dipulihkan')).toBeVisible()
  const names = await apiList(page, `${DIR}/other`)
  expect(names).toContain('b.txt')
  expect(names).toContain('b (1).txt')
})

test('purge selected items permanently, then empty the bin with HAPUS', async ({ page }) => {
  await apiDelete(page, [`${DIR}/sub/c.txt`])
  await page.goto('/trash')
  const row = rowByName(page, 'c.txt')
  await row.click()
  await page.getByTestId('trash-purge').click()
  const dlg = page.getByTestId('delete-permanent')
  await expect(dlg).toBeVisible()
  await dlg.getByTestId('delete-permanent-ok').click()
  await expect(toast(page, '1 item dihapus permanen')).toBeVisible()
  await expect(row).toHaveCount(0)
  expect(await apiList(page, `${DIR}/sub`)).not.toContain('c.txt')
  // empty: two more items, the word is required
  await apiDelete(page, [`${DIR}/sub/d.txt`, `${DIR}/e.txt`])
  await page.goto('/trash')
  await expect(page.locator('[data-testid="trash-row"]')).toHaveCount(2)
  await page.getByTestId('trash-empty').click()
  const empty = page.getByTestId('empty-trash')
  await expect(empty).toBeVisible()
  await expect(empty.getByTestId('empty-trash-ok')).toBeDisabled()
  await empty.getByTestId('empty-trash-input').fill('HAPUS')
  await empty.getByTestId('empty-trash-ok').click()
  await expect(toast(page, /Recycle Bin dikosongkan/)).toBeVisible()
  await expect(page.locator('[data-testid="trash-row"]')).toHaveCount(0)
  await expect(page.getByTestId('trash-status-items')).toContainText('0 item')
  // Shift+Delete in the browser now really deletes (no Recycle Bin row)
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  await fileRow(page, `${DIR}/f.txt`).click()
  await page.keyboard.press('Shift+Delete')
  await page.getByTestId('delete-permanent-ok').click()
  await expect(toast(page, '1 item dihapus permanen')).toBeVisible()
  expect(await apiList(page, DIR)).not.toContain('f.txt')
  await page.goto('/trash')
  await expect(page.locator('[data-testid="trash-row"]')).toHaveCount(0)
})

test('quota of 1 MB rejects copying a 2 MB file with a quota message', async ({ page }) => {
  const big = join(e2eStorageRoot(), 'admin', 'tq', 'big.bin')
  mkdirSync(join(e2eStorageRoot(), 'admin', 'tq'), { recursive: true })
  writeFileSync(big, Buffer.alloc(2 * 1024 * 1024))
  setAdminQuota(1024 * 1024)
  try {
    await page.goto(`/files${DIR}`)
    await expect(page.getByTestId('details-grid')).toBeVisible()
    await expect(page.getByTestId('quota-usage')).toContainText('1 MB')
    await fileRow(page, `${DIR}/big.bin`).click()
    await page.keyboard.press('Control+c')
    await page.goto(`/files${DIR}/other`)
    await expect(page.getByTestId('details-grid')).toBeVisible()
    await page.keyboard.press('Control+v')
    await expect(toast(page, /[Kk]uota/)).toBeVisible()
    expect(await apiList(page, `${DIR}/other`)).not.toContain('big.bin')
  } finally {
    setAdminQuota(10 * 1024 * 1024 * 1024)
  }
})

test('Properties: folder size on demand, checksum tab with verification, "Hitung ukuran" fills the size column', async ({
  page,
}) => {
  await apiCreateFile(page.request, `${DIR}/other`, 'z.txt', 'zzz')
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  await fileRow(page, `${DIR}/other`).click()
  await page.keyboard.press('Alt+Enter')
  const props = page.getByTestId('properties')
  await expect(props).toBeVisible()
  await props.getByTestId('props-compute-size').click()
  await expect(props.getByTestId('props-dir-size-value')).toContainText(/file/)
  await expect(props.getByTestId('props-tab-checksum')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(props).toBeHidden()
  // checksum of a file
  await fileRow(page, `${DIR}/a.txt`).click()
  await page.keyboard.press('Alt+Enter')
  await expect(props).toBeVisible()
  await props.getByTestId('props-tab-checksum').click()
  await props.getByTestId('checksum-compute-all').click()
  const sha = props.getByTestId('checksum-sha256')
  await expect(sha).toHaveText(/^[0-9a-f]{64}$/)
  const digest = (await sha.textContent())!.trim()
  await props.getByTestId('checksum-verify-input').fill(digest.toUpperCase())
  await props.getByTestId('checksum-verify').click()
  await expect(props.getByTestId('checksum-verify-result')).toHaveAttribute('data-match', 'true')
  await props.getByTestId('checksum-verify-input').fill('deadbeef')
  await props.getByTestId('checksum-verify').click()
  await expect(props.getByTestId('checksum-verify-result')).toHaveAttribute('data-match', 'false')
  await page.keyboard.press('Escape')
  // context menu "Hitung ukuran" patches the Ukuran column
  await fileRow(page, `${DIR}/other`).click({ button: 'right' })
  await page.getByTestId('cm-folderSize').click()
  await expect(toast(page, /other:/)).toBeVisible()
  await expect(fileRow(page, `${DIR}/other`)).toContainText(/\d+ (B|KB|MB)/)
})
