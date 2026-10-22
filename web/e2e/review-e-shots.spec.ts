import { expect, test, type APIRequestContext, type Browser, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { DatabaseSync } from 'node:sqlite'
import { join, resolve } from 'node:path'
import {
  ADMIN_NEW_PASSWORD,
  apiResetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

/** Review E UX pass: screenshots (light + dark on 1280×720, light on 390 px)
 *  of the share dialog, the public page states, shared-with-me, my shares,
 *  the admin pages and the profile/sessions page into docs/reviews/review-E/.
 *  Only with REVIEW_E_SHOTS=1; the regressions live in review-e.spec.ts. */
test.describe.configure({ mode: 'serial', timeout: 240_000 })
test.skip(!process.env.REVIEW_E_SHOTS, 'screenshots only on demand (REVIEW_E_SHOTS=1)')

const ORIGIN = 'http://127.0.0.1:18090'
const DIR = '/ux-share'
const OUT = resolve(process.cwd(), '..', 'docs', 'reviews', 'review-E')
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
  method: 'post' | 'delete' = 'post',
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
  return json
}

async function setTheme(page: Page, theme: 'light' | 'dark') {
  await page.evaluate((t) => localStorage.setItem('theme', t), theme)
}

async function shot(page: Page, name: string, variant: string) {
  mkdirSync(OUT, { recursive: true })
  await page.waitForTimeout(400)
  await page.screenshot({ path: join(OUT, `${name}-${variant}.png`), animations: 'disabled' })
}

async function userContext(
  browser: Browser,
  username: string,
  viewport?: { width: number; height: number },
) {
  const ctx = await browser.newContext(viewport ? { viewport } : {})
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
    await seedMkdir(page.request, '/', 'ux-share')
    await seedFile(
      page.request,
      DIR,
      'laporan-tahunan.txt',
      'Laporan tahunan — ringkasan untuk tim.\n',
    )
    await seedFile(page.request, DIR, 'notulen.md', '# Notulen\n\n- agenda satu\n')
    await seedMkdir(page.request, DIR, 'lampiran')
    await seedFile(page.request, `${DIR}/lampiran`, 'data.csv', 'id,nama\n1,Budi\n')
    await seedMkdir(page.request, '/', 'kotak-masuk')
  }
})

test.afterAll(async ({ browser }) => {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await loginAsAdmin(page)
  for (const id of shareIds.splice(0))
    await apiPost(page.request, `/shares/${id}`, undefined, 'delete')
  const token = await csrfToken(page.request)
  await page.request.post('/api/v1/notifications/read', {
    data: { all: true },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  await markNotificationsRead(browser, USER)
  await ctx.close()
})

for (const theme of ['light', 'dark'] as const) {
  test(`screenshots ${theme}`, async ({ page, browser }) => {
    await setTheme(page, theme)
    // share dialog: public link (created) and "ke user" with a recipient chip
    await page.goto('/files/')
    await expect(row(page, DIR)).toBeVisible()
    await row(page, DIR).click({ button: 'right' })
    await page.getByRole('menuitem', { name: /^Share$/ }).click()
    await expect(page.getByTestId('share-dialog')).toBeVisible()
    await page.getByTestId('share-password').fill('rahasia-tim')
    await page.getByTestId('share-max-downloads').fill('5')
    await shot(page, 'share-dialog-form', theme)
    await page.getByTestId('share-create').click()
    await expect(page.getByTestId('share-link')).toBeVisible()
    await page.getByTestId('share-qr-toggle').click()
    await expect(page.getByTestId('share-qr')).toBeVisible()
    await shot(page, 'share-dialog-link', theme)
    // the result view has no tabs: reopen the dialog for "Ke user"
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('share-dialog')).toHaveCount(0)
    await row(page, DIR).click({ button: 'right' })
    await page.getByRole('menuitem', { name: /^Share$/ }).click()
    await expect(page.getByTestId('share-dialog')).toBeVisible()
    await shot(page, 'share-dialog-existing', theme)
    await page.getByTestId('share-tab-users').click()
    if ((await page.getByTestId('recipient-chip').count()) === 0) {
      await page.getByTestId('user-picker-input').first().fill('bu')
      const option = page.getByTestId('user-picker-option').filter({ hasText: 'budi' }).first()
      await expect(option).toBeVisible({ timeout: 10_000 })
      await option.click()
    }
    await shot(page, 'share-dialog-users', theme)
    if (await page.getByTestId('share-users-create').count()) {
      await page.getByTestId('share-users-create').click()
      await expect(page.getByTestId('share-recipients').first()).toContainText('budi', {
        timeout: 10_000,
      })
    }
    await page.keyboard.press('Escape')
    const byPath = await page.request.get(`/api/v1/shares/by-path?path=${encodeURIComponent(DIR)}`)
    for (const it of ((await byPath.json()) as { items: { id: string }[] }).items)
      shareIds.push(it.id)

    // public states
    const folder = await createShare(page.request, { path: DIR, note: 'Bahan rapat minggu depan.' })
    const file = await createShare(page.request, { path: `${DIR}/laporan-tahunan.txt` })
    const locked = await createShare(page.request, {
      path: `${DIR}/lampiran`,
      password: 'rahasia-tim',
    })
    const expired = await createShare(page.request, {
      path: `${DIR}/lampiran`,
      expiresAt: new Date(Date.now() + 2500).toISOString(),
    })
    const request = await createShare(page.request, {
      path: '/kotak-masuk',
      allowUpload: true,
      hideListing: true,
      note: 'Unggah laporan di sini.',
    })
    const gctx = await browser.newContext()
    const guest = await gctx.newPage()
    try {
      await guest.goto(folder.url)
      await setTheme(guest, theme)
      await guest.reload()
      await expect(guest.getByTestId('public-browser')).toBeVisible()
      await shot(guest, 'public-folder', theme)
      await guest
        .locator('[data-testid="public-row"][data-path="/notulen.md"] button')
        .first()
        .click()
      await expect(guest.getByRole('dialog')).toContainText('agenda satu')
      await shot(guest, 'public-viewer', theme)
      await guest.keyboard.press('Escape')
      await guest.goto(file.url)
      await expect(guest.getByTestId('public-file')).toBeVisible()
      await shot(guest, 'public-file', theme)
      await guest.goto(locked.url)
      await expect(guest.getByTestId('public-password-form')).toBeVisible()
      await guest.getByTestId('public-password').fill('salah')
      await guest.getByTestId('public-unlock').click()
      await expect(guest.getByRole('alert')).toBeVisible()
      await shot(guest, 'public-password', theme)
      await guest.waitForTimeout(2600)
      await guest.goto(expired.url)
      await expect(guest.getByText(/kedaluwarsa/i).first()).toBeVisible()
      await shot(guest, 'public-expired', theme)
      await guest.goto(request.url)
      await expect(guest.getByTestId('public-upload')).toBeVisible()
      await shot(guest, 'public-file-request', theme)
    } finally {
      await gctx.close()
    }

    // shared with me (budi) and my shares (admin)
    const { ctx: bctx, page: budi } = await userContext(browser, USER)
    try {
      await setTheme(budi, theme)
      await budi.goto('/shared-with-me')
      await expect(budi.getByTestId('shared-item').first()).toBeVisible()
      await shot(budi, 'shared-with-me', theme)
      await budi.getByTestId('shared-open').first().click()
      await expect(budi.getByTestId('shared-browser')).toBeVisible()
      await shot(budi, 'shared-browser', theme)
    } finally {
      await bctx.close()
    }
    await page.goto('/shares')
    await expect(page.getByTestId('my-share-row').first()).toBeVisible()
    await shot(page, 'my-shares', theme)
    await page.getByTestId('my-share-log').first().click()
    await expect(page.getByTestId('access-log-dialog')).toBeVisible()
    await shot(page, 'my-shares-access-log', theme)
    await page.keyboard.press('Escape')

    // admin
    await page.goto('/admin')
    await expect(page.getByTestId('admin-dashboard')).toBeVisible()
    await page.waitForTimeout(800)
    await shot(page, 'admin-dashboard', theme)
    await page.goto('/admin/users')
    await expect(page.getByTestId('user-row').first()).toBeVisible()
    await shot(page, 'admin-users', theme)
    await page.getByTestId('users-create').click()
    await expect(page.getByTestId('user-create-dialog')).toBeVisible()
    await shot(page, 'admin-user-create', theme)
    await page.keyboard.press('Escape')
    await page.goto('/admin/settings')
    await expect(page.getByTestId('settings-form')).toBeVisible()
    await shot(page, 'admin-settings', theme)
    await page.goto('/admin/audit')
    await expect(page.getByTestId('audit-row').first()).toBeVisible()
    await page.getByTestId('audit-row').first().click()
    await page.waitForTimeout(400)
    await shot(page, 'admin-audit', theme)
    await page.keyboard.press('Escape')
    await page.goto('/admin/maintenance')
    await expect(page.getByTestId('admin-maintenance')).toBeVisible()
    await page.waitForTimeout(600)
    await shot(page, 'admin-maintenance', theme)
    await page.goto('/admin/shares')
    await expect(page.getByTestId('share-row').first()).toBeVisible()
    await shot(page, 'admin-shares', theme)
    await page.goto('/admin/tasks')
    await expect(page.getByTestId('admin-tasks')).toBeVisible()
    await page.waitForTimeout(400)
    await shot(page, 'admin-tasks', theme)

    // profile, sessions, preferences
    await page.goto('/settings')
    await expect(page.getByTestId('sessions')).toBeVisible()
    await shot(page, 'settings-profile', theme)
    await page.getByTestId('preferences').scrollIntoViewIfNeeded()
    await shot(page, 'settings-preferences', theme)

    if (theme === 'light') {
      // 390 px: share dialog, public folder/file/password, shared-with-me, my shares, admin users/settings, settings
      await page.setViewportSize({ width: 390, height: 844 })
      await page.goto('/files/')
      await expect(row(page, DIR)).toBeVisible()
      await row(page, DIR).click({ button: 'right' })
      await page.getByRole('menuitem', { name: /^Share$/ }).click()
      await expect(page.getByTestId('share-dialog')).toBeVisible()
      await shot(page, 'share-dialog', 'mobile')
      await page.keyboard.press('Escape')
      const mctx = await browser.newContext({ viewport: { width: 390, height: 844 } })
      const mguest = await mctx.newPage()
      try {
        await mguest.goto(folder.url)
        await expect(mguest.getByTestId('public-browser')).toBeVisible()
        await shot(mguest, 'public-folder', 'mobile')
        await mguest.goto(file.url)
        await expect(mguest.getByTestId('public-file')).toBeVisible()
        await shot(mguest, 'public-file', 'mobile')
        await mguest.goto(locked.url)
        await expect(mguest.getByTestId('public-password-form')).toBeVisible()
        await shot(mguest, 'public-password', 'mobile')
      } finally {
        await mctx.close()
      }
      const { ctx: mbctx, page: mbudi } = await userContext(browser, USER, {
        width: 390,
        height: 844,
      })
      try {
        await mbudi.goto('/shared-with-me')
        await expect(mbudi.getByTestId('shared-item').first()).toBeVisible()
        await shot(mbudi, 'shared-with-me', 'mobile')
      } finally {
        await mbctx.close()
      }
      await page.goto('/shares')
      await expect(page.getByTestId('my-share-row').first()).toBeVisible()
      await shot(page, 'my-shares', 'mobile')
      await page.goto('/admin/users')
      await expect(page.getByTestId('user-row').first()).toBeVisible()
      await shot(page, 'admin-users', 'mobile')
      await page.goto('/admin/settings')
      await expect(page.getByTestId('settings-form')).toBeVisible()
      await shot(page, 'admin-settings', 'mobile')
      await page.goto('/admin/audit')
      await expect(page.getByTestId('audit-row').first()).toBeVisible()
      await shot(page, 'admin-audit', 'mobile')
      await page.goto('/settings')
      await expect(page.getByTestId('sessions')).toBeVisible()
      await shot(page, 'settings-profile', 'mobile')
      await page.setViewportSize({ width: 1280, height: 720 })
    }
  })
}
