import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { apiMkdir, apiResetPrefs, csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'
import { makePdf } from './pdf-fixtures'

/** Review D (Step R-D) regressions: file names that are XSS payloads on
 *  every surface (grid, breadcrumb, viewer header, Quick Look, editor tab,
 *  toast, task panel, notification, transfer panel, search), the editor
 *  session restoring only files that still exist, the viewer focus trap,
 *  viewer shortcuts versus the PDF find input, and a clean console (CSP,
 *  workers) in the editor, the PDF viewer and the video player. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const ORIGIN = 'http://127.0.0.1:18090'
const DIR = '/xss'
const FFMPEG = '/usr/bin/ffmpeg'

// no quotes or slashes (both are legal in names, but selectors stay simple)
const DIR_NAME = '<img src=x onerror=window.__xss=1>dir'
const TXT = '<img src=x onerror=window.__xss=2>.txt'
const MD = '<svg onload=window.__xss=3>.md'
const JSON_NAME = '<img src=x onerror=window.__xss=5>.json'
const CSV = '<img src=x onerror=window.__xss=7>.csv'
const RENAMED = '<img src=x onerror=window.__xss=9>.txt'
const UPLOAD = '<img src=x onerror=window.__xss=10>.txt'
const ZIP = '<img src=x onerror=window.__xss=11>.zip'

let seeded = false
let home = ''
function seed() {
  if (seeded) return
  seeded = true
  home = join(e2eStorageRoot(), 'admin')
  const dir = join(home, 'xss')
  mkdirSync(join(dir, DIR_NAME), { recursive: true })
  writeFileSync(join(dir, TXT), 'hello needle\nsecond line\n')
  writeFileSync(join(dir, DIR_NAME, 'inside.txt'), 'inside needle\n')
  writeFileSync(
    join(dir, MD),
    '# Judul <b>tebal</b>\n\n<img src=x onerror=window.__xss=4>\n\nteks\n',
  )
  writeFileSync(
    join(dir, JSON_NAME),
    JSON.stringify({ '<img src=x onerror=window.__xss=6>': '<script>x</script>', a: [1, 2] }),
  )
  writeFileSync(join(dir, CSV), 'a,b\n<img src=x onerror=window.__xss=8>,<b>x</b>\n')
  writeFileSync(join(dir, 'exists.txt'), 'exists\n')
  // 2 000 small files: copying them takes > 3 s (DEV_TASK_ITEM_DELAY_MS=2),
  // long enough for the task to produce a notification
  mkdirSync(join(dir, 'banyak'), { recursive: true })
  for (let i = 0; i < 2000; i++) writeFileSync(join(dir, 'banyak', `f${i}.txt`), `${i}\n`)
  writeFileSync(join(dir, 'doc.pdf'), makePdf({ pages: 3, text: (n) => `Halaman ${n} kata-unik` }))
  if (existsSync(FFMPEG)) {
    execFileSync(FFMPEG, [
      '-nostdin',
      '-v',
      'error',
      '-y',
      '-f',
      'lavfi',
      '-i',
      'testsrc=size=160x120:rate=10',
      '-t',
      '2',
      '-c:v',
      'libx264',
      '-pix_fmt',
      'yuv420p',
      '-movflags',
      '+faststart',
      join(dir, 'klip.mp4'),
    ])
  }
}

const row = (page: Page, name: string) =>
  page.locator('[data-testid="details-body"] [role="row"]').filter({ hasText: name }).first()

async function apiPost(page: Page, path: string, data: unknown) {
  const token = await csrfToken(page.request)
  return page.request.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

async function apiPut(page: Page, path: string, data: unknown) {
  const token = await csrfToken(page.request)
  return page.request.put(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

/** No payload ran: window.__xss is never set and no <img src=x> exists. */
async function expectNoXss(page: Page) {
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined()
  expect(await page.locator('img[src="x"]').count()).toBe(0)
  expect(await page.locator('svg[onload]').count()).toBe(0)
}

const consoleErrors: string[] = []
test.beforeEach(async ({ page }) => {
  page.on('console', (m) => {
    if (m.type() === 'error') consoleErrors.push(m.text())
  })
  page.on('pageerror', (e) => consoleErrors.push(`pageerror: ${e.message}`))
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  // earlier specs leave editor tabs behind (editor.spec); start clean
  await apiPut(page, '/me/editor-session', { tabs: [], activePath: null })
  if (!seeded) {
    await apiMkdir(page.request, '/', 'xss')
    seed()
  }
})

test.afterEach(async ({ page }) => {
  await apiPut(page, '/me/editor-session', { tabs: [], activePath: null })
})

test('a file name that is an XSS payload is text on every surface', async ({ page }) => {
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  // grid
  await expect(row(page, TXT)).toContainText(TXT)
  await expect(row(page, DIR_NAME)).toContainText(DIR_NAME)
  await expectNoXss(page)
  // breadcrumb
  await row(page, DIR_NAME).dblclick()
  await expect(page.getByTestId('breadcrumb')).toContainText(DIR_NAME)
  await expectNoXss(page)
  await page.goto(`/files${DIR}`)
  // viewer header + info panel + Quick Look
  await row(page, TXT).dblclick()
  const overlay = page.getByTestId('viewer-overlay')
  await expect(overlay).toBeVisible()
  await expect(page.getByTestId('viewer-name')).toHaveText(TXT)
  await expect(page.getByTestId('code-viewer')).toBeVisible()
  await page.keyboard.press('i')
  await expect(page.getByTestId('viewer-info')).toContainText(`${DIR}/${TXT}`)
  await expectNoXss(page)
  // editor tab from the viewer's Edit button (modal)
  await page.getByTestId('viewer-edit').click()
  await expect(page.getByTestId('editor-modal')).toBeVisible()
  await expect(page.getByTestId('editor-tab').filter({ hasText: TXT })).toBeVisible()
  await expect(page.locator('.monaco-editor .view-lines').first()).toBeVisible({ timeout: 30_000 })
  await expectNoXss(page)
  // Markdown / JSON / CSV viewers render the payloads as text
  await page.goto(`/files${DIR}?view=${encodeURIComponent(`${DIR}/${MD}`)}`)
  await expect(page.getByTestId('markdown-rendered')).toBeVisible()
  await expect(page.getByTestId('markdown-rendered').locator('h1')).toContainText('Judul')
  expect(await page.getByTestId('markdown-rendered').locator('img').count()).toBe(0)
  await expectNoXss(page)
  await page.goto(`/files${DIR}?view=${encodeURIComponent(`${DIR}/${JSON_NAME}`)}`)
  await expect(page.getByTestId('json-tree')).toContainText('<img src=x onerror=window.__xss=6>')
  await expect(page.getByTestId('json-tree')).toContainText('<script>x</script>')
  await expectNoXss(page)
  await page.goto(`/files${DIR}?view=${encodeURIComponent(`${DIR}/${CSV}`)}`)
  await expect(page.getByTestId('csv-viewer')).toContainText('<img src=x onerror=window.__xss=8>')
  await expect(page.getByTestId('csv-viewer')).toContainText('<b>x</b>')
  await expectNoXss(page)
  // Quick Look
  await page.goto(`/files${DIR}`)
  await row(page, TXT).click()
  await page.keyboard.press('Space')
  await expect(page.getByTestId('quick-look')).toBeVisible()
  await expect(page.getByTestId('quick-look-name')).toHaveText(TXT)
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('quick-look')).toHaveCount(0)
  // rename toast
  await row(page, TXT).click()
  await page.keyboard.press('F2')
  const input = page.getByTestId('inline-rename-input')
  await expect(input).toBeVisible()
  await input.fill(RENAMED)
  await page.keyboard.press('Enter')
  await expect(page.getByTestId('undo-toast').last()).toContainText(RENAMED)
  await expect(row(page, RENAMED)).toBeVisible()
  await expectNoXss(page)
  // task panel + notification: compress the payload folder into a payload name
  const res = await apiPost(page, '/archives/compress', {
    paths: [`${DIR}/${DIR_NAME}`],
    format: 'zip',
    name: ZIP,
    dest: DIR,
  })
  expect(res.status(), await res.text()).toBe(202)
  await page.getByTestId('tasks-button').click()
  const panel = page.getByTestId('task-panel')
  await expect(panel).toBeVisible()
  await expect(panel.getByTestId('task-row').first()).toContainText(ZIP)
  await expect(panel.getByTestId('task-row').first()).toHaveAttribute('data-state', 'done', {
    timeout: 30_000,
  })
  await page.getByTestId('task-panel-close').click()
  // a copy that runs > 3 s (tasks.NotifyMinDuration) into the payload folder
  // → notification "Salin 1 item ke /xss/<img …>dir"
  const copy = await apiPost(page, '/files/copy', {
    paths: [`${DIR}/banyak`],
    to: `${DIR}/${DIR_NAME}`,
  })
  expect(copy.status(), await copy.text()).toBe(202)
  await expect(page.getByTestId('notifications-unread')).toHaveText('1', { timeout: 60_000 })
  await page.getByTestId('notifications-bell').click()
  await expect(page.getByTestId('notification-row').first()).toContainText(DIR_NAME)
  await page.keyboard.press('Escape')
  await expectNoXss(page)
  // transfer panel: upload a payload-named file through the real chooser
  const up = join(tmpdir(), 'fm-review-d')
  mkdirSync(up, { recursive: true })
  writeFileSync(join(up, UPLOAD), 'uploaded\n')
  await page.getByTestId('toolbar-upload').click()
  const [chooser] = await Promise.all([
    page.waitForEvent('filechooser'),
    page.getByTestId('toolbar-upload-file').click(),
  ])
  await chooser.setFiles(join(up, UPLOAD))
  const transfer = page.locator('[data-testid="transfer-row"]').filter({ hasText: UPLOAD }).first()
  await expect(transfer).toBeVisible()
  await expect(transfer).toContainText(UPLOAD)
  await expect(row(page, UPLOAD)).toBeVisible({ timeout: 30_000 })
  await expectNoXss(page)
  // search page: names and content
  await page.goto(`/search?path=${encodeURIComponent(DIR)}`)
  await page.getByTestId('search-input').fill('onerror')
  await page.getByTestId('search-submit').click()
  await expect(page.getByTestId('search-name-row').first()).toBeVisible()
  await expect(page.getByTestId('search-name-body')).toContainText('<img src=x onerror=')
  await page.getByTestId('search-tab-content').click()
  await page.getByTestId('search-input').fill('needle')
  await page.getByTestId('search-submit').click()
  await expect(page.getByTestId('search-content-file').first()).toBeVisible()
  await expect(page.getByTestId('search-content-body')).toContainText(RENAMED)
  await expectNoXss(page)
  // editor route
  await page.goto(`/editor?path=${encodeURIComponent(`${DIR}/${RENAMED}`)}`)
  await expect(page.getByTestId('editor-tab').filter({ hasText: RENAMED })).toBeVisible()
  await expect(page.getByTestId('status-location')).toHaveText(`${DIR}/${RENAMED}`)
  await expectNoXss(page)
})

test('editor session: a tab whose file is gone is not restored', async ({ page }) => {
  const res = await apiPut(page, '/me/editor-session', {
    tabs: [
      { path: `${DIR}/exists.txt`, scrollTop: 0 },
      { path: `${DIR}/gone.txt`, scrollTop: 0 },
    ],
    activePath: `${DIR}/gone.txt`,
  })
  expect(res.status(), await res.text()).toBe(200)
  await page.goto('/editor')
  await expect(page.getByTestId('editor-workspace')).toBeVisible()
  await expect(page.locator('.monaco-editor .view-lines').first()).toBeVisible({ timeout: 30_000 })
  await expect(page.getByText('gone.txt tidak ada lagi')).toBeVisible()
  await expect(page.getByTestId('editor-tab')).toHaveCount(1)
  await expect(page.getByTestId('editor-tab').first()).toContainText('exists.txt')
  await expect(page.getByTestId('status-location')).toHaveText(`${DIR}/exists.txt`)
  // the persisted session no longer carries the dead tab (2 s debounce)
  await expect
    .poll(
      async () => {
        const r = await page.request.get('/api/v1/me/editor-session')
        const body = (await r.json()) as { tabs: { path: string }[] }
        return body.tabs.map((t) => t.path)
      },
      { timeout: 10_000 },
    )
    .toEqual([`${DIR}/exists.txt`])
})

test('viewer: focus trap, PDF find input owns the keys, clean console in editor/PDF/video', async ({
  page,
}) => {
  consoleErrors.length = 0
  await page.goto(`/files${DIR}`)
  await row(page, 'doc.pdf').dblclick()
  const overlay = page.getByTestId('viewer-overlay')
  await expect(overlay).toBeVisible()
  await expect(page.getByTestId('pdf-page-count')).toContainText('3', { timeout: 30_000 })
  // focus trap: Tab never leaves the overlay
  for (let i = 0; i < 25; i++) {
    await page.keyboard.press('Tab')
    const inside = await page.evaluate(
      () => !!document.activeElement?.closest('[data-testid="viewer-overlay"]'),
    )
    expect(inside, `Tab #${i + 1} left the overlay`).toBe(true)
  }
  // Ctrl+F focuses the find input; viewer letters typed there are text
  await page.keyboard.press('Control+f')
  const find = page.getByTestId('pdf-search-input')
  await expect(find).toBeFocused()
  await page.keyboard.type('ri0f1')
  await expect(find).toHaveValue('ri0f1')
  expect(await page.getByTestId('viewer-info').count()).toBe(0)
  expect(await page.evaluate(() => document.fullscreenElement)).toBeNull()
  await find.fill('kata-unik')
  await expect(page.getByTestId('pdf-search-count')).toContainText('dari 3', { timeout: 15_000 })
  // Esc in the find box closes the box only; a second Esc closes the viewer
  await page.keyboard.press('Escape')
  await expect(find).toHaveCount(0)
  await expect(overlay).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(overlay).toHaveCount(0)
  // editor (Monaco workers) and the video player (Video.js chunk)
  await page.goto(`/editor?path=${encodeURIComponent(`${DIR}/exists.txt`)}`)
  await expect(page.locator('.monaco-editor .view-lines').first()).toBeVisible({ timeout: 30_000 })
  if (existsSync(FFMPEG)) {
    await page.goto(`/files${DIR}?view=${encodeURIComponent(`${DIR}/klip.mp4`)}`)
    await expect(page.getByTestId('video-player')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId('video-element')).toBeVisible()
    await page.waitForTimeout(1000)
  }
  const bad = consoleErrors.filter((m) =>
    /Content Security Policy|Refused to|worker|pageerror/i.test(m),
  )
  expect(bad).toEqual([])
})
