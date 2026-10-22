import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test'
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs'
import { DatabaseSync } from 'node:sqlite'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

/** Step 19b (SPEC §8.16–§8.18; named after auth.spec in sort order, which
 *  needs the admin still on the initial password — PROMPT calls it
 *  admin-ops.spec.ts): the audit page lists the login and a settings change
 *  (details as JSON), the CSV export downloads with the right columns,
 *  backup_db writes a file into DATA_DIR/backups, integrity_check reports a
 *  folder without an account, and on /settings a user with two sessions
 *  signs the other one out (that session is 401, this one stays).
 *  Everything created (user, tasks, backups, folder, override,
 *  notifications) is removed afterwards. */

test.describe.configure({ mode: 'serial', timeout: 120_000 })

const ORIGIN = 'http://127.0.0.1:18090'
const USERNAME = 'sesi19b'
const PASSWORD = 'Sesi-19b-password-aman!'
const ORPHAN = 'yatim-e2e-19b'
let work = ''
const backupsBefore = new Set<string>()
const ctxs: BrowserContext[] = []

async function apiCall(
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

function db<T>(fn: (d: DatabaseSync) => T): T {
  const d = new DatabaseSync(join(e2eStorageRoot(), '.system', 'db', 'app.db'))
  try {
    d.exec('PRAGMA busy_timeout = 5000')
    return fn(d)
  } finally {
    d.close()
  }
}

function backupsDir() {
  return join(e2eStorageRoot(), '.system', 'backups')
}

function listBackups(): string[] {
  return existsSync(backupsDir()) ? readdirSync(backupsDir()) : []
}

async function waitTask(page: Page, id: string) {
  await expect
    .poll(
      async () => {
        const r = await page.request.get('/api/v1/admin/tasks?limit=50')
        const body = (await r.json()) as { items: { id: string; state: string }[] }
        return body.items.find((i) => i.id === id)?.state
      },
      { timeout: 30_000 },
    )
    .toMatch(/done|failed|cancelled/)
}

async function newSession(browser: Browser, password: string): Promise<Page> {
  const ctx = await browser.newContext()
  ctxs.push(ctx)
  const page = await ctx.newPage()
  const res = await page.request.post('/api/v1/auth/login', {
    data: { username: USERNAME, password },
    headers: { Origin: ORIGIN },
  })
  expect(res.status()).toBe(200)
  return page
}

async function cleanup(page: Page) {
  // the test user (purge) and its delete task
  const list = await page.request.get(`/api/v1/admin/users?q=${USERNAME}`)
  if (list.ok()) {
    const body = (await list.json()) as { items: { id: string; username: string }[] }
    const u = body.items.find((i) => i.username === USERNAME)
    if (u) {
      const del = await apiCall(
        page,
        `/admin/users/${u.id}`,
        { confirmUsername: USERNAME, mode: 'purge' },
        'delete',
      )
      if (del.status() === 202) {
        const task = ((await del.json()) as { task: { id: string } }).task
        await waitTask(page, task.id)
        await apiCall(page, `/tasks/${task.id}`, undefined, 'delete')
      }
    }
  }
  await apiCall(page, '/admin/settings/reset', { keys: ['activity_retention_days'] })
  await apiCall(page, '/notifications/read', { all: true })
  // system tasks created by this spec have no owner-facing delete endpoint
  db((d) =>
    d
      .prepare(
        `DELETE FROM tasks WHERE user_id IS NULL AND type IN ('backup_db', 'integrity_check') AND created_at >= ?`,
      )
      .run(startedAt),
  )
  for (const name of listBackups()) {
    if (!backupsBefore.has(name)) rmSync(join(backupsDir(), name), { force: true })
  }
  rmSync(join(e2eStorageRoot(), ORPHAN), { recursive: true, force: true })
}

let startedAt = ''

test.beforeAll(() => {
  work = mkdtempSync(join(tmpdir(), 'fm-e2e-ops-'))
  startedAt = new Date(Date.now() - 1000).toISOString()
  for (const n of listBackups()) backupsBefore.add(n)
})

test.afterAll(async ({ browser }) => {
  for (const c of ctxs) await c.close()
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await loginAsAdmin(page)
  await cleanup(page)
  await ctx.close()
  rmSync(work, { recursive: true, force: true })
})

test('audit log lists the login and a settings change; details in the drawer', async ({ page }) => {
  await loginAsAdmin(page)
  const put = await apiCall(page, '/admin/settings', { activity_retention_days: 30 }, 'put')
  expect(put.status()).toBe(200)
  await page.goto('/admin/audit')
  await page.getByTestId('audit-action').selectOption('settings.')
  const row = page.locator('[data-testid="audit-row"][data-action="settings.update"]').first()
  await expect(row).toBeVisible()
  await row.click()
  const json = page.getByTestId('audit-detail-json')
  await expect(json).toContainText('"key": "activity_retention_days"')
  await expect(json).toContainText('"new": 30')
  await page.keyboard.press('Escape')
  await page.getByTestId('audit-action').selectOption('auth.')
  await expect(
    page.locator('[data-testid="audit-row"][data-action="auth.login"]').first(),
  ).toBeVisible()
  await expect(page.locator('[data-testid="audit-row"]:not([data-action^="auth."])')).toHaveCount(0)
})

test('CSV export downloads with BOM, header and the filtered rows', async ({ page }) => {
  await loginAsAdmin(page)
  await page.goto('/admin/audit')
  await page.getByTestId('audit-action').selectOption('auth.')
  // the link follows the (debounced) filter
  await expect(page.getByTestId('audit-export')).toHaveAttribute('href', /action=auth\./)
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByTestId('audit-export').click(),
  ])
  expect(download.suggestedFilename()).toMatch(/^audit-\d{8}-\d{6}\.csv$/)
  const saved = join(work, 'audit.csv')
  await download.saveAs(saved)
  const text = readFileSync(saved, 'utf8')
  expect(text.charCodeAt(0)).toBe(0xfeff)
  const lines = text.slice(1).split('\r\n')
  expect(lines[0]).toBe('id,at,actor,actor_id,action,target_type,target_id,ip,user_agent,details')
  const data = lines.slice(1).filter((l) => l !== '')
  expect(data.length).toBeGreaterThan(0)
  for (const l of data.slice(0, 20)) expect(l.split(',')[4]).toMatch(/^auth\./)
})

test('maintenance: backup_db writes a file, integrity_check finds a folder without an account', async ({
  page,
}) => {
  await loginAsAdmin(page)
  mkdirSync(join(e2eStorageRoot(), ORPHAN), { recursive: true })
  await page.goto('/admin/maintenance')
  const backup = page.getByTestId('maint-card-backup_db')
  await expect(backup).toBeVisible()
  await expect(backup).toContainText('daily 02:30')
  await page.getByTestId('maint-run-backup_db').click()
  await page.getByTestId('maint-run-confirm').click()
  await expect(backup.getByTestId('maint-state')).toHaveText('Selesai', { timeout: 30_000 })
  const created = listBackups().filter((n) => !backupsBefore.has(n))
  expect(created).toHaveLength(1)
  expect(created[0]).toMatch(/^app-\d{8}-\d{6}\.db$/)
  expect(readFileSync(join(backupsDir(), created[0]!)).subarray(0, 15).toString()).toBe(
    'SQLite format 3',
  )
  await expect(page.getByTestId('backup-row').filter({ hasText: created[0]! })).toBeVisible()

  const integrity = page.getByTestId('maint-card-integrity_check')
  await page.getByTestId('maint-run-integrity_check').click()
  await page.getByTestId('maint-run-confirm').click()
  await expect(integrity.getByTestId('maint-state')).toHaveText('Selesai', { timeout: 30_000 })
  await expect(integrity.getByTestId('integrity-orphanFolders')).toContainText(ORPHAN)
})

test('profile: two sessions, sign the other out → it gets 401, this one stays', async ({
  page,
  browser,
}) => {
  await loginAsAdmin(page)
  const created = await apiCall(page, '/admin/users', {
    username: USERNAME,
    displayName: 'Sesi 19b',
    role: 'user',
    temporaryPassword: 'Sementara-19b-aman!',
  })
  expect(created.status()).toBe(201)
  // first login + forced password change (revokes nothing else yet)
  const a = await newSession(browser, 'Sementara-19b-aman!')
  const change = await a.request.post('/api/v1/auth/change-password', {
    data: { currentPassword: 'Sementara-19b-aman!', newPassword: PASSWORD },
    headers: { 'X-CSRF-Token': await csrfToken(a.request), Origin: ORIGIN },
  })
  expect(change.status()).toBe(204)
  const b = await newSession(browser, PASSWORD)
  await a.goto('/settings')
  await expect(a.getByTestId('profile-page')).toBeVisible()
  const rows = a.getByTestId('session-row')
  await expect(rows).toHaveCount(2)
  await expect(a.locator('[data-testid="session-row"][data-current="true"]')).toHaveCount(1)
  await a
    .locator('[data-testid="session-row"][data-current="false"]')
    .getByTestId('session-logout')
    .click()
  await a.getByTestId('session-confirm-confirm').click()
  await expect(rows).toHaveCount(1)
  expect((await b.request.get('/api/v1/auth/me')).status()).toBe(401)
  expect((await a.request.get('/api/v1/auth/me')).status()).toBe(200)
  // "sign out every other session" with a fresh third device
  const c = await newSession(browser, PASSWORD)
  await a.reload()
  await expect(rows).toHaveCount(2)
  await a.getByTestId('sessions-revoke-others').click()
  await a.getByTestId('session-confirm-confirm').click()
  await expect(rows).toHaveCount(1)
  expect((await c.request.get('/api/v1/auth/me')).status()).toBe(401)
  // the preference form saves through me/preferences
  await a.getByTestId('pref-theme').selectOption('dark')
  await expect(a.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect
    .poll(async () => {
      const me = await a.request.get('/api/v1/me/preferences')
      return ((await me.json()) as { prefs: Record<string, unknown> }).prefs['ui.theme']
    })
    .toBe('dark')
})
