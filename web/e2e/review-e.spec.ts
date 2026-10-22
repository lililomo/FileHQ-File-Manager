import AxeBuilder from '@axe-core/playwright'
import { expect, test, type APIRequestContext, type Browser, type Page } from '@playwright/test'
import { DatabaseSync } from 'node:sqlite'
import { join } from 'node:path'
import {
  ADMIN_NEW_PASSWORD,
  apiResetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

/** Review E regressions (PROMPT Step R-E): the public page loads only the
 *  chunks it needs; a hidden owner never reaches the guest's DOM; a custom
 *  slug and "Ganti link" work through the dialog; axe at 390 px on the
 *  public page; a single-file share offers no rename to its editor. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const ORIGIN = 'http://127.0.0.1:18090'
const DIR = '/review-e'
const USER = 'budi'
let seeded = false
const shareIds: string[] = []

function ensureUser(username: string, displayName: string) {
  const db = new DatabaseSync(join(e2eStorageRoot(), '.system', 'db', 'app.db'))
  try {
    db.exec('PRAGMA busy_timeout = 5000')
    if (db.prepare('SELECT 1 FROM users WHERE username = ?').get(username)) return
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

async function apiPost(
  request: APIRequestContext,
  path: string,
  data: unknown,
  method: 'post' | 'patch' | 'delete' = 'post',
) {
  const token = await csrfToken(request)
  return request[method](`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

async function createShare(request: APIRequestContext, body: Record<string, unknown>) {
  const res = await apiPost(request, '/shares', body)
  expect(res.status(), await res.text()).toBe(201)
  const json = (await res.json()) as { share: { id: string }; url: string }
  shareIds.push(json.share.id)
  return { id: json.share.id, url: json.url, token: json.url.slice(json.url.lastIndexOf('/') + 1) }
}

async function userContext(browser: Browser, username: string) {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await page.goto('/login')
  await page.getByLabel('Username').fill(username)
  await page.getByLabel('Password', { exact: true }).fill(ADMIN_NEW_PASSWORD)
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page).toHaveURL(/\/files/)
  return { ctx, page }
}

/** Seed helpers tolerate a re-run in a restarted worker (409 = already there). */
async function seedMkdir(request: APIRequestContext, path: string, name: string) {
  const token = await csrfToken(request)
  const res = await request.post('/api/v1/files/mkdir', {
    data: { path, name },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect([201, 409], await res.text()).toContain(res.status())
}
async function seedFile(request: APIRequestContext, path: string, name: string, content: string) {
  const token = await csrfToken(request)
  const res = await request.post('/api/v1/files/create', {
    data: { path, name, content },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect([201, 409], await res.text()).toContain(res.status())
}

/** Marks every notification of `username` read through a throwaway API session
 *  (share_received/share_revoked land on the recipient; later specs count badges). */
async function markNotificationsRead(browser: Browser, username: string) {
  const ctx = await browser.newContext()
  try {
    const login = await ctx.request.post('/api/v1/auth/login', {
      data: { username, password: ADMIN_NEW_PASSWORD },
      headers: { Origin: ORIGIN },
    })
    if (!login.ok()) return
    const token = await csrfToken(ctx.request)
    await ctx.request.post('/api/v1/notifications/read', {
      data: { all: true },
      headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
    })
  } finally {
    await ctx.close()
  }
}

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(300)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    ensureUser(USER, 'Budi')
    await seedMkdir(page.request, '/', 'review-e')
    await seedFile(page.request, DIR, 'catatan.txt', 'isi catatan untuk review E')
    await seedFile(page.request, DIR, 'kedua.txt', 'file kedua')
  }
})

test.afterEach(async ({ page, browser }) => {
  for (const id of shareIds.splice(0))
    await apiPost(page.request, `/shares/${id}`, undefined, 'delete')
  const token = await csrfToken(page.request)
  await page.request.post('/api/v1/notifications/read', {
    data: { all: true },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  await markNotificationsRead(browser, USER)
})

test('public page loads no application chunk and passes axe at 390 px', async ({
  page,
  browser,
}) => {
  const share = await createShare(page.request, { path: DIR })
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 } })
  const guest = await ctx.newPage()
  const scripts: string[] = []
  guest.on('request', (req) => {
    const url = req.url()
    if (url.includes('/assets/') && url.endsWith('.js'))
      scripts.push(url.slice(url.lastIndexOf('/') + 1))
  })
  try {
    await guest.goto(share.url)
    await expect(guest.getByTestId('public-browser')).toBeVisible()
    await expect(guest.getByTestId('public-row')).toHaveCount(2)
    // open the text viewer (the only feature chunk a guest needs besides the page)
    await guest
      .locator('[data-testid="public-row"][data-path="/catatan.txt"] button')
      .first()
      .click()
    await expect(guest.getByRole('dialog')).toContainText('isi catatan untuk review E')
    await guest.waitForTimeout(500)
    // feature chunks a guest must never pull (lucide icon chunks such as users-*.js are fine)
    const forbidden = scripts.filter((n) =>
      /^(admin|audit|maintenance|settings|ShareDialog|SharedBrowser|shared-with-me|shares|editor|editor\.api|vs|pdf|pdf_viewer|VideoPlayer|qrcode|Dashboard|UsersPage|AuditPage|MaintenancePage|SharesPage|TasksPage|ProfilePage)-/.test(
        n,
      ),
    )
    console.log(`review-e public chunks (${scripts.length}): ${[...new Set(scripts)].join(', ')}`)
    expect(forbidden, `application chunks on /s/*: ${forbidden.join(', ')}`).toEqual([])
    await guest.keyboard.press('Escape')
    const axe = await new AxeBuilder({ page: guest }).analyze()
    expect(axe.violations.filter((v) => v.impact === 'critical' || v.impact === 'serious')).toEqual(
      [],
    )
    expect(
      await guest.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  } finally {
    await ctx.close()
  }
})

test('a hidden owner never appears in the guest DOM, a custom slug and "Ganti link" work through the dialog', async ({
  page,
  browser,
}) => {
  // budi owns the folder and hides himself
  const { ctx: bctx, page: budi } = await userContext(browser, USER)
  try {
    await seedMkdir(budi.request, '/', 'induk-pribadi')
    await seedMkdir(budi.request, '/induk-pribadi', 'kotak-tamu')
    await seedFile(budi.request, '/induk-pribadi/kotak-tamu', 'laporan.txt', 'laporan untuk tamu')
    const res = await apiPost(budi.request, '/shares', {
      path: '/induk-pribadi/kotak-tamu',
      hideOwner: true,
      allowUpload: true,
    })
    expect(res.status()).toBe(201)
    const json = (await res.json()) as { share: { id: string }; url: string }
    const gctx = await browser.newContext()
    const guest = await gctx.newPage()
    try {
      await guest.goto(json.url)
      await expect(guest.getByTestId('public-browser')).toBeVisible()
      await expect(guest.getByTestId('public-owner')).toHaveCount(0)
      const html = await guest.content()
      expect(html).not.toMatch(/budi/i)
      expect(html).not.toContain('induk-pribadi')
    } finally {
      await gctx.close()
      await apiPost(budi.request, `/shares/${json.share.id}`, undefined, 'delete')
      // leave budi's quota untouched for share-users.spec (used must be 0)
      await apiPost(budi.request, '/files/delete-permanent', { paths: ['/induk-pribadi'] })
    }
  } finally {
    await bctx.close()
  }

  // admin: slug through the dialog, then "Ganti link" drops it
  await page.goto('/files/')
  await expect(row(page, DIR)).toBeVisible()
  await row(page, DIR).click({ button: 'right' })
  await page.getByRole('menuitem', { name: /^Share$/ }).click()
  await expect(page.getByTestId('share-dialog')).toBeVisible()
  await page.getByTestId('share-slug').fill('Tim-Review-E')
  await page.getByTestId('share-create').click()
  const link = page.getByTestId('share-link')
  await expect(link).toBeVisible()
  const slugUrl = await link.inputValue()
  expect(slugUrl).toMatch(/\/s\/tim-review-e$/)
  const list = await page.request.get(`/api/v1/shares/by-path?path=${encodeURIComponent(DIR)}`)
  const items = ((await list.json()) as { items: { id: string; slug: string | null }[] }).items
  const created = items.find((i) => i.slug === 'tim-review-e')
  expect(created).toBeTruthy()
  shareIds.push(created!.id)
  await page.keyboard.press('Escape')
  const gctx = await browser.newContext()
  const guest = await gctx.newPage()
  try {
    await guest.goto(slugUrl)
    await expect(guest.getByTestId('public-browser')).toBeVisible()
    // manage from /shares → Ganti link → confirm
    await page.goto('/shares')
    const myRow = page.getByTestId('my-share-row').filter({ hasText: '/review-e' }).first()
    await expect(myRow).toBeVisible()
    await myRow.getByTestId('my-share-manage').click()
    await expect(page.getByTestId('share-dialog')).toBeVisible()
    await page.getByRole('button', { name: 'Ganti link' }).click()
    await page.getByTestId('share-confirm').click()
    await expect
      .poll(async () => {
        const r = await page.request.get(`/api/v1/shares/${created!.id}`)
        return ((await r.json()) as { share: { url: string; slug: string | null } }).share
      })
      .toMatchObject({ slug: null })
    const fresh = await page.request.get(`/api/v1/shares/${created!.id}`)
    const url = ((await fresh.json()) as { share: { url: string } }).share.url
    expect(url).toMatch(/\/s\/[A-Za-z0-9_-]{43}$/)
    await guest.goto(slugUrl)
    await expect(guest.getByText('Link tidak ditemukan')).toBeVisible()
    await guest.goto(url)
    await expect(guest.getByTestId('public-browser')).toBeVisible()
  } finally {
    await gctx.close()
  }
})

test('a single-file share offers its editor no rename; a folder share does', async ({
  page,
  browser,
}) => {
  const file = await apiPost(page.request, '/shares', {
    path: `${DIR}/catatan.txt`,
    mode: 'users',
    recipients: [{ username: USER, permission: 'editor' }],
  })
  expect(file.status(), await file.text()).toBe(201)
  const fileId = ((await file.json()) as { share: { id: string } }).share.id
  shareIds.push(fileId)
  const folder = await apiPost(page.request, '/shares', {
    path: DIR,
    mode: 'users',
    recipients: [{ username: USER, permission: 'editor' }],
  })
  const folderId = ((await folder.json()) as { share: { id: string } }).share.id
  shareIds.push(folderId)
  const { ctx, page: budi } = await userContext(browser, USER)
  try {
    await budi.goto(`/shared-with-me/${fileId}/`)
    await expect(budi.getByTestId('shared-browser')).toBeVisible()
    await budi.getByTestId('shared-check').first().click()
    await expect(budi.getByTestId('shared-rename')).toHaveCount(0)
    const r = await apiPost(budi.request, `/files/rename?share=${fileId}`, {
      path: '/',
      newName: 'lain.txt',
    })
    expect(r.status()).toBe(400)
    await budi.goto(`/shared-with-me/${folderId}/`)
    await expect(budi.getByTestId('shared-browser')).toBeVisible()
    await budi.getByTestId('shared-check').first().click()
    await expect(budi.getByTestId('shared-rename')).toBeVisible()
  } finally {
    await ctx.close()
  }
})
