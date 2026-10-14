import { expect, test, type Page } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { apiMkdir, apiResetPrefs, csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

/** Step 11 AC: a 5 000-file copy runs as a task with live progress, can be
 *  cancelled (destination left clean), retried to completion and produces a
 *  notification. The backend runs with DEV_TASK_ITEM_DELAY_MS=2 (backend.mjs)
 *  so the copy is slow enough to observe. */
test.describe.configure({ mode: 'serial', timeout: 180_000 })

const DIR = '/tasks'
const ORIGIN = 'http://127.0.0.1:18090'
const FILES = 5000
let seeded = false

async function apiList(page: Page, path: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(path)}`)
  if (res.status() === 404) return []
  expect(res.status()).toBe(200)
  return ((await res.json()) as { items: { name: string }[] }).items.map((i) => i.name)
}

/** Entry count of a folder (`total` of the first page: listings page at 500). */
async function apiCount(page: Page, path: string): Promise<number> {
  const res = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(path)}`)
  if (res.status() === 404) return -1
  expect(res.status()).toBe(200)
  return ((await res.json()) as { total: number }).total
}

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)
const toast = (page: Page, text: string | RegExp) =>
  page.locator('[data-sonner-toaster]').getByText(text)

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'tasks')
    await apiMkdir(page.request, DIR, 'tujuan')
    const src = join(e2eStorageRoot(), 'admin', 'tasks', 'banyak')
    mkdirSync(src, { recursive: true })
    for (let i = 0; i < FILES; i++)
      writeFileSync(join(src, `f${String(i).padStart(4, '0')}.txt`), 'x')
  }
})

test('large copy → task with progress → cancel leaves the destination clean → retry completes → notification', async ({
  page,
}) => {
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  await expect(row(page, `${DIR}/banyak`)).toBeVisible()

  // Ctrl+C on the big folder, Ctrl+V inside tujuan → 202 task
  await row(page, `${DIR}/banyak`).click()
  await page.keyboard.press('Control+c')
  await row(page, `${DIR}/tujuan`).dblclick()
  await expect(page).toHaveURL(new RegExp(`/files${DIR}/tujuan$`))
  await page.keyboard.press('Control+v')
  await expect(toast(page, /dijalankan sebagai task/)).toBeVisible()

  // panel opens with the running task and a live progress bar
  const panel = page.getByTestId('task-panel')
  await expect(panel).toBeVisible()
  const taskRow = panel.getByTestId('task-row').first()
  await expect(taskRow).toHaveAttribute('data-state', 'running')
  await expect(taskRow.getByTestId('task-progress')).toBeVisible()
  await expect(page.getByTestId('tasks-count')).toHaveText('1')
  await expect
    .poll(async () => (await taskRow.getByTestId('task-meta').textContent()) ?? '', {
      timeout: 15_000,
    })
    .toMatch(/B|KB|MB/)

  // cancel mid-way: state cancelled, destination folder gone, retry offered
  await taskRow.getByTestId('task-cancel').click()
  await expect(taskRow).toHaveAttribute('data-state', 'cancelled', { timeout: 20_000 })
  await expect(toast(page, /dibatalkan/)).toBeVisible()
  await expect.poll(() => apiList(page, `${DIR}/tujuan`)).toEqual([])
  await expect(page.getByTestId('tasks-count')).toHaveCount(0)

  // retry → a new task appears first, finishes, listing refreshes via file.changed
  const firstId = (await taskRow.getAttribute('data-task-id')) ?? ''
  await taskRow.getByTestId('task-retry').click()
  const retried = panel.getByTestId('task-row').first()
  await expect(retried).not.toHaveAttribute('data-task-id', firstId)
  await expect(retried).toHaveAttribute('data-state', /queued|running/)
  await expect(retried).toHaveAttribute('data-state', 'done', { timeout: 120_000 })
  await expect(toast(page, /selesai/)).toBeVisible()
  await expect(row(page, `${DIR}/tujuan/banyak`)).toBeVisible({ timeout: 15_000 })
  await expect.poll(() => apiCount(page, `${DIR}/tujuan/banyak`)).toBe(FILES)

  // notification bell shows the finished task and marks it read
  const bell = page.getByTestId('notifications-bell')
  await expect(page.getByTestId('notifications-unread')).toHaveText('1')
  await bell.click()
  const note = page.getByTestId('notification-row').first()
  await expect(note).toHaveAttribute('data-unread', 'true')
  await expect(note).toContainText(/Salin/)
  await page.getByTestId('notifications-read-all').click()
  await expect(page.getByTestId('notifications-unread')).toHaveCount(0)
  await page.keyboard.press('Escape')

  // the done row can be removed from the list; the panel closes
  await retried.getByTestId('task-remove').click()
  await expect(panel.getByTestId('task-row')).toHaveCount(1)
  await page.getByTestId('task-panel-close').click()
  await expect(panel).toBeHidden()
  // Tasks button reopens it, panel survives a route change
  await page.getByTestId('tasks-button').click()
  await expect(panel).toBeVisible()
  await page.getByTestId('nav-trash').click() // client-side navigation keeps the panel mounted
  await expect(page).toHaveURL(/\/trash$/)
  await expect(page.getByTestId('task-panel')).toBeVisible()
})

test('the SSE stream reconnects after the server-side connection cap closes the oldest tab', async ({
  page,
  context,
}) => {
  // six tabs: the broker keeps five, the first reconnects (badge still updates)
  const pages = [page]
  for (let i = 0; i < 5; i++) {
    const p = await context.newPage()
    await p.goto('/files')
    await expect(p.getByTestId('details-grid')).toBeVisible()
    pages.push(p)
  }
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/files/copy', {
    data: { paths: [`${DIR}/banyak`], to: `${DIR}/tujuan`, onConflict: 'rename' },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(202)
  await expect(page.getByTestId('tasks-count')).toHaveText('1', { timeout: 15_000 })
  await expect(page.getByTestId('tasks-count')).toHaveCount(0, { timeout: 120_000 })
  for (const p of pages.slice(1)) await p.close()
})
