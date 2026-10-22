import { expect, test, type Browser, type Page } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { DatabaseSync } from 'node:sqlite'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  ADMIN_NEW_PASSWORD,
  apiCreateFile,
  apiMkdir,
  apiResetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

/** Step 18b (SPEC §8.15): A shares a folder with B (viewer) → B finds it in
 *  "Dibagikan ke saya", browses, downloads, cannot write; A promotes B to
 *  editor → B uploads and edits a file in the editor; A sees the change; the
 *  scope cannot escape the folder; a non-recipient gets 404; A revokes → B is
 *  told and loses access. Two browser contexts: A (admin) and B (budi). */

test.describe.configure({ mode: 'serial', timeout: 180_000 })

const ORIGIN = 'http://127.0.0.1:18090'
const DIR = '/tim'
let seeded = false

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)

/** Users other than admin are inserted straight into SQLite (no admin API
 *  before Step 19a): same password hash as the admin → same password. */
function ensureUser(username: string, displayName: string) {
  const db = new DatabaseSync(join(e2eStorageRoot(), '.system', 'db', 'app.db'))
  try {
    db.exec('PRAGMA busy_timeout = 5000')
    const exists = db.prepare('SELECT 1 FROM users WHERE username = ?').get(username)
    if (exists) return
    db.prepare(
      `INSERT INTO users (id, username, display_name, role, status, password_hash, must_change_password,
         password_changed_at, quota_bytes, created_at, updated_at)
       SELECT lower(hex(randomblob(16))), ?, ?, 'user', 'active', password_hash, 0, password_changed_at,
         quota_bytes, created_at, updated_at FROM users WHERE username = 'admin'`,
    ).run(username, displayName)
  } finally {
    db.close()
  }
}

async function loginAs(page: Page, username: string) {
  await page.goto('/login')
  await page.getByLabel('Username').fill(username)
  await page.getByLabel('Password', { exact: true }).fill(ADMIN_NEW_PASSWORD)
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page).toHaveURL(/\/files/)
}

async function userContext(browser: Browser, username: string) {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await loginAs(page, username)
  return { ctx, page }
}

async function apiGet(page: Page, path: string) {
  return page.request.get(`/api/v1${path}`)
}
async function apiPost(page: Page, path: string, data: unknown) {
  const token = await csrfToken(page.request)
  return page.request.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'tim')
    await apiCreateFile(page.request, DIR, 'catatan.txt', 'catatan tim versi satu')
    await apiCreateFile(page.request, DIR, 'data.csv', 'a,b\n1,2\n')
    await apiMkdir(page.request, DIR, 'arsip')
    await apiCreateFile(page.request, '/', 'pribadi.txt', 'milik admin saja')
    ensureUser('budi', 'Budi')
    ensureUser('citra', 'Citra')
  }
})

test.afterEach(async ({ page }) => {
  // the owner's share notifications (received/upload) must not leak into
  // tasks.spec, which counts the unread badge from a clean state
  const token = await csrfToken(page.request)
  await page.request.post('/api/v1/notifications/read', {
    data: { all: true },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
})

test('A shares a folder with B as viewer: B browses and downloads, cannot write; promoted to editor B uploads and edits; revoke ends it', async ({
  page,
  browser,
}) => {
  // --- A: share dialog → tab "Ke user" → budi (viewer) ----------------------
  await page.goto('/files/')
  await expect(row(page, DIR)).toBeVisible()
  await row(page, DIR).click({ button: 'right' })
  await page.getByRole('menuitem', { name: /^Share$/ }).click()
  const dialog = page.getByTestId('share-dialog')
  await expect(dialog).toBeVisible()
  await page.getByTestId('share-tab-users').click()
  await expect(page.getByTestId('share-no-users')).toBeVisible()
  await page.getByTestId('user-picker-input').fill('bu')
  const option = page.getByTestId('user-picker-option').filter({ hasText: 'budi' })
  await expect(option).toBeVisible()
  await option.click()
  await expect(page.getByTestId('recipient-chip')).toHaveAttribute('data-username', 'budi')
  await page.getByTestId('share-users-create').click()
  await expect(page.getByTestId('share-row').filter({ hasText: 'budi' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(row(page, DIR).locator('[data-badge="shared"]')).toBeVisible()

  // --- B: notification + "Dibagikan ke saya" ----------------------------------
  const b = await userContext(browser, 'budi')
  const c = await userContext(browser, 'citra')
  try {
    await expect(b.page.getByTestId('notifications-unread')).toHaveText('1', { timeout: 15_000 })
    await b.page.getByTestId('notifications-bell').click()
    await expect(b.page.getByTestId('notification-row').first()).toContainText('membagikan folder')
    await b.page.keyboard.press('Escape')
    await b.page.getByTestId('nav-sharedWithMe').click()
    const item = b.page.getByTestId('shared-item')
    await expect(item).toHaveCount(1)
    await expect(item).toContainText('tim')
    await expect(b.page.getByTestId('shared-perm')).toHaveText('Viewer')
    await b.page.getByTestId('shared-open').click()
    await expect(b.page).toHaveURL(/\/shared-with-me\/[^/]+\/?$/)
    await expect(b.page.getByTestId('shared-page')).toHaveAttribute('data-permission', 'viewer')
    const shareId = b.page.url().match(/shared-with-me\/([^/?]+)/)![1]!
    await expect(b.page.getByTestId('shared-row')).toHaveCount(3)
    // viewer: no write controls, download works (recipient-bound signed URL)
    await expect(b.page.getByTestId('shared-upload')).toHaveCount(0)
    await expect(b.page.getByTestId('shared-mkdir')).toHaveCount(0)
    const sign = await apiPost(b.page, `/files/sign?share=${shareId}`, {
      path: '/catatan.txt',
      purpose: 'download',
    })
    expect(sign.status(), await sign.text()).toBe(200)
    const signed = (await sign.json()) as { url: string }
    expect(signed.url).toContain('scope=r%3A')
    const dl = await b.page.request.get(signed.url)
    expect(dl.status()).toBe(200)
    expect(await dl.text()).toBe('catatan tim versi satu')
    // viewer cannot write through the API either
    const pre = await apiPost(b.page, `/upload/share/${shareId}/precheck`, {
      path: '/',
      files: [{ name: 'x.txt', size: 1 }],
    })
    expect(pre.status()).toBe(403)
    const mk = await apiPost(b.page, `/files/mkdir?share=${shareId}`, { path: '/', name: 'nope' })
    expect(mk.status()).toBe(403)
    // the scope cannot leave the folder; the owner's path never appears
    const esc = await apiGet(b.page, `/files/list?share=${shareId}&path=..%2F`)
    expect(esc.status()).toBe(400)
    const list = await apiGet(b.page, `/files/list?share=${shareId}&path=/`)
    expect(list.status()).toBe(200)
    expect(await list.text()).not.toContain('/tim')
    // C is not a recipient: 404 everywhere
    const cList = await apiGet(c.page, `/files/list?share=${shareId}&path=/`)
    expect(cList.status()).toBe(404)
    const cHeader = await apiGet(c.page, `/shared-with-me/${shareId}`)
    expect(cHeader.status()).toBe(404)

    // --- A: promote budi to editor in the dialog -----------------------------
    await page.goto('/files/')
    await row(page, DIR).click({ button: 'right' })
    await page.getByRole('menuitem', { name: /^Share$/ }).click()
    await page.getByTestId('share-tab-users').click()
    const chip = page.getByTestId('recipient-chip').filter({ hasText: 'budi' })
    await expect(chip).toBeVisible()
    await chip.getByTestId('recipient-perm').click()
    await page.getByRole('option', { name: 'Editor' }).click()
    await page.getByTestId('share-recipients-save').click()
    await expect(
      page.locator('[data-sonner-toaster]').getByText('Penerima diperbarui'),
    ).toBeVisible()
    await page.keyboard.press('Escape')

    // --- B (editor): upload through the share tus route, edit in the editor ----
    await b.page.reload()
    await expect(b.page.getByTestId('shared-page')).toHaveAttribute('data-permission', 'editor')
    const up = join(tmpdir(), 'fm-e2e-share-users')
    mkdirSync(up, { recursive: true })
    writeFileSync(join(up, 'unggahan.txt'), 'dari budi')
    await b.page.getByTestId('shared-upload-input-file').setInputFiles(join(up, 'unggahan.txt'))
    await expect(b.page.getByTestId('shared-row').filter({ hasText: 'unggahan.txt' })).toBeVisible({
      timeout: 30_000,
    })
    // the owner's quota, not budi's, carries the bytes
    const me = await apiGet(b.page, '/me/quota')
    if (me.status() === 200) {
      const q = (await me.json()) as { used?: number }
      expect(q.used ?? 0).toBe(0)
    }
    // edit catatan.txt in the editor (scope via ?share=)
    await b.page
      .getByTestId('shared-row')
      .filter({ hasText: 'catatan.txt' })
      .getByTestId('shared-row-edit')
      .click()
    await expect(b.page).toHaveURL(/\/editor\?.*share=/)
    const editorTab = b.page.getByTestId('editor-tab').first()
    await expect(editorTab).toContainText('catatan.txt')
    await b.page.locator('.monaco-editor .view-lines').click()
    await b.page.keyboard.press('Control+End')
    await b.page.keyboard.type(' — diedit budi')
    await expect(b.page.getByTestId('tab-dirty')).toHaveCount(1)
    await b.page.keyboard.press('Control+s')
    await expect(b.page.getByText('catatan.txt disimpan')).toBeVisible({ timeout: 15_000 })
    await expect(b.page.getByTestId('tab-dirty')).toHaveCount(0)
    // the versions of the file live with the owner: A sees them by absolute path
    const vers = await apiGet(page, '/versions?path=/tim/catatan.txt')
    expect(vers.status()).toBe(200)
    expect(((await vers.json()) as { items: unknown[] }).items.length).toBeGreaterThanOrEqual(1)

    // --- A sees budi's work in /tim; the activity names budi ---------------
    const content = await apiGet(page, '/files/content?path=/tim/catatan.txt')
    expect(await content.text()).toContain('diedit budi')
    const own = await apiGet(page, '/files/list?path=/tim')
    expect(await own.text()).toContain('unggahan.txt')
    await expect(page.getByTestId('notifications-unread')).not.toHaveText('0')
    const notes = await apiGet(page, '/notifications')
    expect(await notes.text()).toContain('budi mengunggah')
    const logs = await apiGet(page, `/shares/${shareId}/access-logs`)
    expect(await logs.text()).toContain('"username":"budi"')

    // --- A revokes budi: B is told and loses access -----------------------------
    await page.goto('/shares')
    const myRow = page.getByTestId('my-share-row').filter({ hasText: 'tim' })
    await expect(myRow).toBeVisible()
    await expect(myRow.getByTestId('my-share-recipients')).toContainText('budi · Editor')
    await myRow.getByTestId('my-share-manage').click()
    await page.getByTestId('share-tab-users').click()
    await page
      .getByTestId('recipient-chip')
      .filter({ hasText: 'budi' })
      .getByRole('button', { name: /Hapus budi/ })
      .click()
    // the last recipient cannot be saved as an empty set → remove the share instead
    await expect(page.getByTestId('share-recipients-save')).toBeDisabled()
    await page.getByTestId('share-delete').click()
    await page.getByTestId('share-confirm').click()
    await expect(page.locator('[data-sonner-toaster]').getByText('Link dihapus')).toBeVisible()
    await page.keyboard.press('Escape')
    const gone = await apiGet(b.page, `/files/list?share=${shareId}&path=/`)
    expect(gone.status()).toBe(404)
    await b.page.getByTestId('nav-sharedWithMe').click()
    await expect(b.page.getByTestId('shared-item')).toHaveCount(0)
    const bNotes = await apiGet(b.page, '/notifications')
    expect(await bNotes.text()).toContain('share_revoked')
  } finally {
    await b.ctx.close()
    await c.ctx.close()
  }
})

test('a single shared file: path "/" is the file; siblings and listing are unreachable', async ({
  page,
  browser,
}) => {
  const res = await apiPost(page, '/shares', {
    path: '/tim/data.csv',
    mode: 'users',
    recipients: [{ username: 'citra', permission: 'viewer' }],
  })
  expect(res.status(), await res.text()).toBe(201)
  const shareId = ((await res.json()) as { share: { id: string } }).share.id
  const c = await userContext(browser, 'citra')
  try {
    await c.page.goto(`/shared-with-me/${shareId}`)
    await expect(c.page.getByTestId('shared-title')).toHaveText('data.csv')
    await expect(c.page.getByTestId('shared-row')).toHaveCount(1)
    const stat = await apiGet(c.page, `/files/stat?share=${shareId}&path=/`)
    expect(stat.status()).toBe(200)
    expect(((await stat.json()) as { name: string }).name).toBe('data.csv')
    const sibling = await apiGet(c.page, `/files/content?share=${shareId}&path=/catatan.txt`)
    expect(sibling.status()).toBe(404)
    const list = await apiGet(c.page, `/files/list?share=${shareId}&path=/`)
    expect(list.status()).toBe(400)
    // leave the share
    await c.page.getByRole('button', { name: 'Dibagikan ke saya' }).click()
    await c.page.getByTestId('shared-leave').click()
    await c.page.getByTestId('shared-leave-confirm').click()
    await expect(c.page.getByTestId('shared-item')).toHaveCount(0)
    const after = await apiGet(c.page, `/files/stat?share=${shareId}&path=/`)
    expect(after.status()).toBe(404)
  } finally {
    await c.ctx.close()
  }
})
