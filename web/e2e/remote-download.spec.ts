import { expect, test, type Page } from '@playwright/test'
import { createHash, randomBytes } from 'node:crypto'
import { createServer, type Server } from 'node:http'
import { apiMkdir, apiResetPrefs, csrfToken, loginAsAdmin } from './fixtures'

/** Step 13 AC through the UI: a local HTTP server stands in for the
 *  internet (backend.mjs sets DEV_REMOTE_DOWNLOAD_ALLOW_LOOPBACK=true), the
 *  dialog validates URLs client-side, each URL becomes a task with
 *  progress/speed in the panel, the file lands with an identical checksum,
 *  a redirect to the metadata address fails with SSRF_BLOCKED and a link-
 *  local literal is refused before anything is queued. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/remote'
const ORIGIN = 'http://127.0.0.1:18090'
const MB = 1024 * 1024
let seeded = false
let server: Server
let base = ''
const payload = randomBytes(6 * MB)
const payloadSha = createHash('sha256').update(payload).digest('hex')

test.beforeAll(async () => {
  server = createServer((req, res) => {
    if (req.url?.startsWith('/files/paket.bin')) {
      res.writeHead(200, {
        'Content-Type': 'application/octet-stream',
        'Content-Length': payload.length,
        'Accept-Ranges': 'bytes',
      })
      // trickle so the panel can show progress
      let off = 0
      const tick = () => {
        if (off >= payload.length) return res.end()
        const chunk = payload.subarray(off, off + 64 * 1024)
        off += chunk.length
        res.write(chunk, () => setTimeout(tick, 20))
      }
      tick()
      return
    }
    if (req.url === '/to-metadata') {
      res.writeHead(302, { Location: 'http://169.254.169.254/latest/meta-data/' })
      return res.end()
    }
    if (req.url === '/named') {
      res.writeHead(200, {
        'Content-Disposition': `attachment; filename*=UTF-8''laporan%20akhir.txt`,
        'Content-Length': 5,
      })
      return res.end('hello')
    }
    res.writeHead(404)
    res.end()
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const addr = server.address()
  if (!addr || typeof addr === 'string') throw new Error('no address')
  base = `http://127.0.0.1:${addr.port}`
})

test.afterAll(async () => {
  await new Promise<void>((resolve) => server.close(() => resolve()))
})

async function apiSha256(page: Page, path: string): Promise<string> {
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/files/checksum', {
    data: { path, algos: ['sha256'] },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
  return ((await res.json()) as { sha256: string }).sha256
}

/** Later suites (tasks.spec) count the rows of the task panel, so every
 *  test here removes the final tasks it created. */
async function apiDeleteFinalTasks(page: Page): Promise<void> {
  const token = await csrfToken(page.request)
  const res = await page.request.get('/api/v1/tasks?limit=50')
  expect(res.status()).toBe(200)
  const { items } = (await res.json()) as { items: { id: string; state: string }[] }
  for (const t of items) {
    if (t.state === 'queued' || t.state === 'running') continue
    const del = await page.request.delete(`/api/v1/tasks/${t.id}`, {
      headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
    })
    expect(del.status(), await del.text()).toBe(204)
  }
}

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)
const toast = (page: Page, text: string | RegExp) =>
  page.locator('[data-sonner-toaster]').getByText(text)

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(300)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'remote')
  }
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('toolbar-remote')).toBeVisible()
})

test('toolbar Remote → dialog validates URLs → task with progress → file with identical checksum', async ({
  page,
}) => {
  await page.getByTestId('toolbar-remote').click()
  const dialog = page.getByTestId('remote-dialog')
  await expect(dialog).toBeVisible()
  await expect(page.getByTestId('remote-dest')).toHaveValue(DIR)

  // client-side validation: scheme, userinfo, too many
  const urls = page.getByTestId('remote-urls')
  await urls.fill('ftp://example.com/x')
  await expect(page.getByTestId('remote-urls-error')).toContainText(/http/)
  await expect(page.getByTestId('remote-submit')).toBeDisabled()
  await urls.fill('http://user:pw@example.com/x')
  await expect(page.getByTestId('remote-urls-error')).toContainText(/password/)
  await urls.fill(Array.from({ length: 11 }, (_, i) => `http://example.com/${i}`).join('\n'))
  await expect(page.getByTestId('remote-urls-error')).toContainText(/10/)

  // probe fills the name for a single URL
  await urls.fill(`${base}/named`)
  await page.getByTestId('remote-probe').click()
  await expect(page.getByTestId('remote-probe-result')).toContainText('5')
  await expect(page.getByTestId('remote-filename')).toHaveValue('laporan akhir.txt')

  // the real download with a query string that must never show up anywhere
  await urls.fill(`${base}/files/paket.bin?token=RAHASIA`)
  await page.getByTestId('remote-filename').fill('')
  await page.getByTestId('remote-submit').click()
  await expect(dialog).toBeHidden()
  await expect(toast(page, /dijalankan sebagai task/)).toBeVisible()

  const panel = page.getByTestId('task-panel')
  await expect(panel).toBeVisible()
  const taskRow = panel.getByTestId('task-row').first()
  await expect(taskRow).toContainText('paket.bin')
  await expect(taskRow).not.toContainText('RAHASIA')
  // bytes-based progress while it runs (the server trickles 64 KB / 20 ms),
  // then done with the total size
  await expect
    .poll(async () => (await taskRow.getByTestId('task-meta').textContent()) ?? '', {
      timeout: 20_000,
    })
    .toMatch(/MB/)
  await expect(taskRow).toHaveAttribute('data-state', 'done', { timeout: 60_000 })
  await expect(taskRow.getByTestId('task-meta')).toContainText('6 MB')
  await expect(toast(page, /selesai/)).toBeVisible()
  await expect(row(page, `${DIR}/paket.bin`)).toBeVisible({ timeout: 15_000 })
  expect(await apiSha256(page, `${DIR}/paket.bin`)).toBe(payloadSha)
  await apiDeleteFinalTasks(page)
})

test('redirect to the metadata address fails the task with SSRF_BLOCKED; a link-local literal is refused up front', async ({
  page,
}) => {
  await page.getByTestId('toolbar-remote').click()
  const urls = page.getByTestId('remote-urls')
  await urls.fill(`${base}/to-metadata`)
  await page.getByTestId('remote-submit').click()
  const panel = page.getByTestId('task-panel')
  const taskRow = panel.getByTestId('task-row').first()
  await expect(taskRow).toHaveAttribute('data-state', 'failed', { timeout: 30_000 })
  await expect(taskRow.getByTestId('task-meta')).toContainText(/tidak diizinkan|not allowed/)
  await expect(toast(page, /gagal/)).toBeVisible()

  // literal link-local: the API refuses before queueing; the dialog shows the reason
  await page.getByTestId('toolbar-remote').click()
  await page.getByTestId('remote-urls').fill('http://169.254.169.254/latest/meta-data/')
  await page.getByTestId('remote-submit').click()
  await expect(page.getByTestId('remote-error')).toContainText(/tidak diizinkan|privat/)
  await expect(page.getByTestId('remote-dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('remote-dialog')).toBeHidden()
  await apiDeleteFinalTasks(page)
})

test('context menu on the empty area offers Remote download', async ({ page }) => {
  const grid = page.getByTestId('details-grid')
  await expect(grid).toBeVisible()
  const box = (await grid.boundingBox())!
  await grid.click({ button: 'right', position: { x: box.width / 2, y: box.height - 12 } })
  const menu = page.getByRole('menu')
  await expect(menu).toHaveAttribute('data-context', 'background')
  await expect(page.getByTestId('cm-remoteDownload')).toBeVisible()
  await page.getByTestId('cm-remoteDownload').click()
  await expect(page.getByTestId('remote-dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('remote-dialog')).toBeHidden()
})
