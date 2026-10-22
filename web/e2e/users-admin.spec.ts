import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test'
import { randomBytes } from 'node:crypto'
import { existsSync, mkdtempSync, rmSync, statfsSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

/** Step 19a (SPEC §8.17; file sorts after auth.spec, which needs the admin
 *  still on the initial password): admin creates budi with a temporary password →
 *  budi logs in, is forced to change it, the home folder appears → admin
 *  sets a 1 MB quota → budi's 2 MB upload is refused → admin disables budi
 *  (session dies) → enables → admin cannot demote itself (last admin) →
 *  `share_require_password` override makes budi's share dialog require a
 *  password → dashboard disk capacity matches statfs. Everything the spec
 *  creates (user, task, notifications, override) is removed afterwards. */

test.describe.configure({ mode: 'serial', timeout: 180_000 })

const ORIGIN = 'http://127.0.0.1:18090'
const USERNAME = 'budi19a'
const BUDI_PASSWORD = 'Budi-password-baru-2026!'
const MB = 1024 * 1024
let tempPassword = ''
let work = ''
let budiCtx: BrowserContext | null = null
let budiPage: Page | null = null

async function apiPost(
  page: Page,
  path: string,
  data: unknown,
  method: 'post' | 'put' | 'delete' | 'patch' = 'post',
) {
  const token = await csrfToken(page.request)
  return page.request[method](`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

async function loginBudi(browser: Browser, password: string): Promise<Page> {
  if (budiCtx) await budiCtx.close()
  budiCtx = await browser.newContext()
  budiPage = await budiCtx.newPage()
  await budiPage.goto('/login')
  await budiPage.getByLabel('Username').fill(USERNAME)
  await budiPage.getByLabel('Password', { exact: true }).fill(password)
  await budiPage.getByRole('button', { name: 'Masuk' }).click()
  return budiPage
}

/** Cleanup that must survive a failed run: purge the user (task), drop the
 *  task row and its notification, reset the settings override. */
async function cleanup(page: Page) {
  const list = await page.request.get(`/api/v1/admin/users?q=${USERNAME}`)
  if (list.ok()) {
    const body = (await list.json()) as { items: { id: string; username: string }[] }
    const u = body.items.find((i) => i.username === USERNAME)
    if (u) {
      const del = await apiPost(
        page,
        `/admin/users/${u.id}`,
        { confirmUsername: USERNAME, mode: 'purge' },
        'delete',
      )
      if (del.status() === 202) {
        const task = ((await del.json()) as { task: { id: string } }).task
        for (let i = 0; i < 50; i++) {
          const t = await page.request.get(`/api/v1/tasks/${task.id}`)
          const state = ((await t.json()) as { task: { state: string } }).task.state
          if (state === 'done' || state === 'failed') break
          await page.waitForTimeout(200)
        }
        await apiPost(page, `/tasks/${task.id}`, undefined, 'delete')
      }
    }
  }
  await apiPost(page, '/admin/settings/reset', { keys: ['share_require_password'] })
  await apiPost(page, '/notifications/read', { all: true })
}

test.beforeAll(() => {
  work = mkdtempSync(join(tmpdir(), 'fm-e2e-admin-'))
})

test.afterAll(async ({ browser }) => {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await loginAsAdmin(page)
  await cleanup(page)
  await ctx.close()
  if (budiCtx) await budiCtx.close()
  rmSync(work, { recursive: true, force: true })
})

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
})

test('admin creates budi with a one-time temporary password; a non-admin gets 403', async ({
  page,
}) => {
  await page.goto('/admin/users')
  await expect(page.getByTestId('admin-users')).toBeVisible()
  await page.getByTestId('users-create').click()
  const dialog = page.getByTestId('user-create-dialog')
  await expect(dialog).toBeVisible()
  // reserved / invalid names are refused by the form and by the server
  await page.getByTestId('user-username').fill('www')
  await page.getByTestId('user-displayname').fill('Budi')
  await page.getByTestId('user-create-submit').click()
  await expect(page.getByTestId('user-username-error')).toBeVisible()
  await page.getByTestId('user-username').fill(USERNAME)
  await page.getByTestId('user-create-submit').click()
  const once = page.getByTestId('password-once-dialog')
  await expect(once).toBeVisible()
  tempPassword = (await page.getByTestId('temporary-password').innerText()).trim()
  expect(tempPassword).toHaveLength(16)
  await page.getByTestId('password-done').click()
  await expect(once).toBeHidden()
  const row = page.locator(`[data-testid="user-row"][data-username="${USERNAME}"]`)
  await expect(row).toBeVisible()
  await expect(row).toContainText('Wajib ganti password')
  // the password never appears in the list response
  const list = await page.request.get(`/api/v1/admin/users?q=${USERNAME}`)
  expect(await list.text()).not.toContain(tempPassword)
  // the folder does not exist before the first login
  expect(existsSync(join(e2eStorageRoot(), USERNAME))).toBe(false)
})

test('budi logs in with the temporary password, must change it, then has a home folder', async ({
  browser,
}) => {
  const b = await loginBudi(browser, tempPassword)
  await expect(b).toHaveURL(/\/change-password/)
  await b.getByLabel('Password saat ini').fill(tempPassword)
  await b.getByLabel('Password baru', { exact: true }).fill(BUDI_PASSWORD)
  await b.getByLabel('Ulangi password baru').fill(BUDI_PASSWORD)
  await b.getByRole('button', { name: 'Simpan password' }).click()
  await expect(b).toHaveURL(/\/files/)
  expect(existsSync(join(e2eStorageRoot(), USERNAME))).toBe(true)
  // a non-admin is refused by the server, not only by the hidden menu
  const forbidden = await b.request.get('/api/v1/admin/users')
  expect(forbidden.status()).toBe(403)
  await b.goto('/admin/users')
  await expect(b).toHaveURL(/\/files/)
  await expect(b.getByTestId('admin-layout')).toHaveCount(0)
})

test('admin sets a 1 MB quota; budi cannot upload 2 MB', async ({ page }) => {
  await page.goto('/admin/users')
  const row = page.locator(`[data-testid="user-row"][data-username="${USERNAME}"]`)
  await row.getByTestId('user-menu').click()
  await page.getByTestId('user-action-edit').click()
  const dialog = page.getByTestId('user-edit-dialog')
  await expect(dialog).toBeVisible()
  await page.getByTestId('user-quota').fill('0.001')
  await page.getByTestId('user-edit-submit').click()
  await expect(dialog).toBeHidden()
  await expect(row).toContainText('1 MB')
  // budi: a 2 MB upload is refused before any tus request
  const b = budiPage!
  const file = join(work, 'besar.bin')
  writeFileSync(file, randomBytes(2 * MB))
  let tusRequests = 0
  b.on('request', (req) => {
    if (req.url().includes('/api/v1/upload/tus')) tusRequests++
  })
  await b.goto('/files/')
  await b.getByTestId('toolbar-upload').click()
  const [chooser] = await Promise.all([
    b.waitForEvent('filechooser'),
    b.getByTestId('toolbar-upload-file').click(),
  ])
  await chooser.setFiles(file)
  const precheck = b.getByTestId('upload-precheck')
  await expect(precheck).toBeVisible()
  await expect(precheck).toContainText('Kuota tidak cukup')
  await expect(b.getByTestId('upload-precheck-continue')).toHaveCount(0)
  await b.getByTestId('upload-precheck-cancel').click()
  expect(tusRequests).toBe(0)
})

test("disable kills budi's session; enable lets budi back in", async ({ page, browser }) => {
  await page.goto('/admin/users')
  const row = page.locator(`[data-testid="user-row"][data-username="${USERNAME}"]`)
  await row.getByTestId('user-menu').click()
  await page.getByTestId('user-action-disable').click()
  await page.getByTestId('user-confirm-disable-confirm').click()
  await expect(row).toHaveAttribute('data-status', 'disabled')
  const b = budiPage!
  const me = await b.request.get('/api/v1/auth/me')
  expect([401, 403]).toContain(me.status())
  await b.goto('/files/')
  await expect(b).toHaveURL(/\/login/)
  // login while disabled is refused
  await b.getByLabel('Username').fill(USERNAME)
  await b.getByLabel('Password', { exact: true }).fill(BUDI_PASSWORD)
  await b.getByRole('button', { name: 'Masuk' }).click()
  await expect(b.getByRole('alert')).toBeVisible()
  await expect(b).toHaveURL(/\/login/)
  // enable
  await row.getByTestId('user-menu').click()
  await page.getByTestId('user-action-enable').click()
  await expect(row).toHaveAttribute('data-status', 'active')
  const back = await loginBudi(browser, BUDI_PASSWORD)
  await expect(back).toHaveURL(/\/files/)
})

test('the last admin cannot demote itself', async ({ page }) => {
  await page.goto('/admin/users')
  const row = page.locator('[data-testid="user-row"][data-username="admin"]')
  await row.getByTestId('user-menu').click()
  await expect(page.getByTestId('user-action-disable')).toBeDisabled()
  await expect(page.getByTestId('user-action-delete')).toBeDisabled()
  await page.getByTestId('user-action-edit').click()
  const dialog = page.getByTestId('user-edit-dialog')
  await page.getByTestId('user-role').click()
  await page.getByRole('option', { name: 'User' }).click()
  await page.getByTestId('user-edit-submit').click()
  await expect(page.getByTestId('user-role-error')).toContainText('Admin aktif terakhir')
  await expect(dialog).toBeVisible()
  await page.keyboard.press('Escape')
  // the server says 409 last_admin too
  const admin = (await (await page.request.get('/api/v1/auth/me')).json()) as {
    user: { id: string }
  }
  const res = await apiPost(page, `/admin/users/${admin.user.id}`, { role: 'user' }, 'patch')
  expect(res.status()).toBe(409)
  expect(await res.text()).toContain('last_admin')
})

test("share_require_password override applies at once to budi's share dialog", async ({ page }) => {
  await page.goto('/admin/settings')
  await expect(page.getByTestId('admin-settings')).toBeVisible()
  const source = page.getByTestId('setting-source-share_require_password')
  await expect(source).toHaveAttribute('data-source', /default|env/)
  await page.getByTestId('setting-input-share_require_password').click()
  await expect(page.getByTestId('settings-dirty')).toContainText('1')
  // an invalid size is refused with the key named
  await page.getByTestId('setting-input-max_upload_size').fill('banyak')
  await expect(page.getByTestId('setting-error-max_upload_size')).toBeVisible()
  await page.getByTestId('setting-input-max_upload_size').fill('20GB')
  await page.getByTestId('settings-save').click()
  await expect(source).toHaveAttribute('data-source', 'override')
  await expect(page.getByTestId('setting-reset-share_require_password')).toBeVisible()
  // budi: a fresh session sees the effective config and the dialog requires a password
  const b = budiPage!
  const create = await apiPost(b, '/files/create', {
    path: '/',
    name: 'catatan.txt',
    content: 'isi',
  })
  expect(create.status()).toBe(201)
  await b.goto('/files/')
  const row = b.locator('[data-testid="details-body"] [role="row"][data-path="/catatan.txt"]')
  await expect(row).toBeVisible()
  await row.click({ button: 'right' })
  await b.getByRole('menuitem', { name: /^Share$/ }).click()
  await expect(b.getByTestId('share-dialog')).toBeVisible()
  await expect(b.getByTestId('share-password')).toHaveAttribute('required', '')
  await b.getByTestId('share-create').click()
  await expect(b.getByTestId('share-password')).toBeFocused()
  await b.keyboard.press('Escape')
  // reset from the settings page: back to the env value
  await page.getByTestId('setting-reset-share_require_password').click()
  await expect(source).toHaveAttribute('data-source', /default|env/)
})

test('dashboard shows the real disk capacity and the dependency table', async ({ page }) => {
  await page.goto('/admin')
  await expect(page.getByTestId('admin-dashboard')).toBeVisible()
  await expect(page.getByTestId('deps-table').locator('tr[data-tool="ffmpeg"]')).toBeVisible()
  await expect(page.getByTestId('stat-version')).toBeVisible()
  const api = (await (await page.request.get('/api/v1/admin/dashboard')).json()) as {
    users: { active: number }
    storage: { disk: { total: number } }
    deps: { name: string }[]
  }
  // other specs may have created accounts: the card shows the live count (admin + budi at least)
  expect(api.users.active).toBeGreaterThanOrEqual(2)
  await expect(page.getByTestId('stat-users-active')).toContainText(String(api.users.active))
  const st = statfsSync(e2eStorageRoot())
  const total = Number(st.blocks) * Number(st.bsize)
  expect(Math.abs(api.storage.disk.total - total) / total).toBeLessThan(0.01)
  expect(api.deps.map((d) => d.name)).toEqual([
    'ffmpeg',
    'ffprobe',
    'vipsthumbnail',
    'pdftoppm',
    '7z',
    'rg',
    'unrar',
    'rar',
  ])
})

test("delete (archive) moves the folder to deleted-users and ends budi's session", async ({
  page,
}) => {
  await page.goto('/admin/users')
  const row = page.locator(`[data-testid="user-row"][data-username="${USERNAME}"]`)
  await row.getByTestId('user-menu').click()
  await page.getByTestId('user-action-delete').click()
  const dialog = page.getByTestId('user-delete-dialog')
  await expect(dialog).toBeVisible()
  await expect(page.getByTestId('user-delete-submit')).toBeDisabled()
  await page.getByTestId('user-delete-confirm').fill(USERNAME)
  await page.getByTestId('user-delete-archive').click()
  await page.getByTestId('user-delete-submit').click()
  await expect(dialog).toBeHidden()
  await expect(row).toHaveCount(0)
  // the task finishes and the folder is archived
  await expect
    .poll(async () => existsSync(join(e2eStorageRoot(), USERNAME)), { timeout: 15_000 })
    .toBe(false)
  const tasks = (await (await page.request.get('/api/v1/tasks?state=done')).json()) as {
    items: { id: string; type: string; result?: { archivedTo?: string } }[]
  }
  const del = tasks.items.find((t) => t.type === 'delete_user')
  expect(del?.result?.archivedTo).toMatch(new RegExp(`^deleted-users/${USERNAME}-`))
  expect(
    existsSync(join(e2eStorageRoot(), '.system', del!.result!.archivedTo!, 'catatan.txt')),
  ).toBe(true)
  const me = await budiPage!.request.get('/api/v1/auth/me')
  expect([401, 403]).toContain(me.status())
  // tidy: the task row and its notification must not leak into tasks.spec
  await apiPost(page, `/tasks/${del!.id}`, undefined, 'delete')
  await apiPost(page, '/notifications/read', { all: true })
  rmSync(join(e2eStorageRoot(), '.system', del!.result!.archivedTo!), {
    recursive: true,
    force: true,
  })
})
