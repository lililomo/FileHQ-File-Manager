import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { existsSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import {
  apiCreateFile,
  apiMkdir,
  apiResetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

/** Step 14b AC through the UI: open a zip like a folder ("Lihat isi arsip"),
 *  walk into a folder, preview a text member streamed straight out of the
 *  archive (nothing in DATA_DIR/tmp), extract two selected members, add a
 *  file and remove a member (zip rewritten, verified with `unzip -l` /
 *  `unzip -t`), and a password zip that asks once, keeps the password for
 *  previews and never puts it in the URL. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/jelajah'
const ZIP = `${DIR}/paket.zip`
const ORIGIN = 'http://127.0.0.1:18090'
let seeded = false

const fileRow = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)
const entryRow = (page: Page, inner: string) =>
  page.locator(`[data-testid="archive-body"] [role="row"][data-path="${inner}"]`)

async function apiPost(page: Page, path: string, data: unknown) {
  const token = await csrfToken(page.request)
  return page.request.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

async function apiTaskDone(page: Page, id: string): Promise<void> {
  await expect
    .poll(
      async () => {
        const res = await page.request.get(`/api/v1/tasks/${id}`)
        return ((await res.json()) as { task: { state: string } }).task.state
      },
      { timeout: 60_000 },
    )
    .toBe('done')
}

async function apiList(page: Page, path: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(path)}`)
  expect(res.status(), await res.text()).toBe(200)
  const { items } = (await res.json()) as { items: { name: string }[] }
  return items.map((i) => i.name).sort()
}

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

async function waitDone(page: Page, text: string | RegExp) {
  const panel = page.getByTestId('task-panel')
  await expect(panel).toBeVisible()
  const taskRow = panel.getByTestId('task-row').filter({ hasText: text }).first()
  await expect(taskRow).toHaveAttribute('data-state', 'done', { timeout: 60_000 })
  return taskRow
}

async function openBrowser(page: Page, archive = ZIP) {
  await page.goto(`/files${DIR}?archive=${encodeURIComponent(archive)}`)
  const browser = page.getByTestId('archive-browser')
  await expect(browser).toBeVisible()
  await expect(entryRow(page, 'docs')).toBeVisible()
  return browser
}

function tmpWorkspaces(): string[] {
  const tmp = join(e2eStorageRoot(), '.system', 'tmp')
  if (!existsSync(tmp)) return []
  return readdirSync(tmp).filter((n) => n.startsWith('ar-'))
}

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(300)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'jelajah')
    await apiMkdir(page.request, DIR, 'sumber')
    await apiMkdir(page.request, `${DIR}/sumber`, 'docs')
    await apiMkdir(page.request, `${DIR}/sumber/docs`, 'deep')
    await apiCreateFile(page.request, `${DIR}/sumber`, 'readme.txt', 'baca saya dulu\n'.repeat(20))
    await apiCreateFile(page.request, `${DIR}/sumber`, 'tabel.csv', 'a,b\n'.repeat(1000))
    await apiCreateFile(page.request, `${DIR}/sumber/docs`, 'guide.txt', 'panduan lengkap arsip\n')
    await apiCreateFile(page.request, `${DIR}/sumber/docs/deep`, 'notes.md', '# catatan\n')
    await apiCreateFile(page.request, DIR, 'tambahan.txt', 'file tambahan\n')
    const res = await apiPost(page, '/archives/compress', {
      paths: [`${DIR}/sumber/readme.txt`, `${DIR}/sumber/tabel.csv`, `${DIR}/sumber/docs`],
      format: 'zip',
      name: 'paket',
      destPath: DIR,
    })
    expect(res.status(), await res.text()).toBe(202)
    await apiTaskDone(page, ((await res.json()) as { task: { id: string } }).task.id)
    await apiDeleteFinalTasks(page)
  }
})

test('open a zip from the context menu, walk into a folder, preview a text member', async ({
  page,
}) => {
  await page.goto(`/files${DIR}`)
  const archive = fileRow(page, ZIP)
  await expect(archive).toBeVisible({ timeout: 15_000 })
  await archive.click({ button: 'right' })
  await page.getByTestId('cm-browseArchive').click()
  const browser = page.getByTestId('archive-browser')
  await expect(browser).toBeVisible()
  await expect(page).toHaveURL(/[?&]archive=/)
  await expect(page.getByTestId('archive-title')).toHaveText('paket.zip')
  await expect(page.getByTestId('archive-status')).toContainText('3 item')

  // root: folders first, then natural name order
  const rows = page.getByTestId('archive-row')
  await expect(rows).toHaveCount(3)
  await expect(rows.nth(0)).toHaveAttribute('data-path', 'docs')
  await expect(rows.nth(1)).toHaveAttribute('data-path', 'readme.txt')
  await expect(rows.nth(2)).toHaveAttribute('data-path', 'tabel.csv')
  await expect(entryRow(page, 'docs')).toContainText('2 item')

  // double-click enters the folder: breadcrumb, Up button, children
  await entryRow(page, 'docs').dblclick()
  await expect(entryRow(page, 'docs/guide.txt')).toBeVisible()
  await expect(entryRow(page, 'docs/deep')).toBeVisible()
  await expect(page.getByTestId('archive-crumb')).toHaveText(['docs'])
  await expect(page.getByTestId('archive-up')).toBeEnabled()

  // a single click on a text member previews it, streamed out of the archive
  await entryRow(page, 'docs/guide.txt').click()
  await expect(page.getByTestId('archive-preview-text')).toContainText('panduan lengkap arsip')
  expect(tmpWorkspaces(), 'preview must not extract anything to DATA_DIR/tmp').toEqual([])

  // Up, then Escape closes the overlay and drops ?archive= from the URL
  await page.getByTestId('archive-up').click()
  await expect(entryRow(page, 'readme.txt')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(browser).toBeHidden()
  await expect(page).not.toHaveURL(/archive=/)
})

test('extract two selected members to a new folder: only those two land there', async ({
  page,
}) => {
  await openBrowser(page)
  await entryRow(page, 'readme.txt').click()
  await entryRow(page, 'docs').click({ modifiers: ['Control'] })
  await expect(page.getByTestId('archive-status-selected')).toContainText('2')
  await page.getByTestId('archive-extract-selected').click()
  const dialog = page.getByTestId('extract-entries-dialog')
  await expect(dialog).toBeVisible()
  await expect(page.getByTestId('extract-entries-new')).toBeChecked()
  await page.getByTestId('extract-entries-submit').click()
  await expect(dialog).toBeHidden()
  await waitDone(page, 'paket.zip')

  expect(await apiList(page, `${DIR}/paket`)).toEqual(['docs', 'readme.txt'])
  expect(await apiList(page, `${DIR}/paket/docs`)).toEqual(['deep', 'guide.txt'])
  expect(await apiList(page, `${DIR}/paket/docs/deep`)).toEqual(['notes.md'])
  await apiDeleteFinalTasks(page)
})

test('add a file and remove a member: the zip is rewritten and verified with unzip', async ({
  page,
}) => {
  await openBrowser(page)
  await expect(page.getByTestId('archive-add')).toBeEnabled()
  await page.getByTestId('archive-add').click()
  const dialog = page.getByTestId('archive-add-dialog')
  await expect(dialog).toBeVisible()
  // expand /jelajah in the picker and double-click tambahan.txt to put it on the list
  await page.getByTestId(`picker-node-${DIR}`).getByRole('button').click()
  await page.getByTestId(`picker-node-${DIR}/tambahan.txt`).dblclick()
  await expect(page.getByTestId('archive-add-list')).toContainText(`${DIR}/tambahan.txt`)
  await page.getByTestId('archive-add-submit').click()
  await expect(dialog).toBeHidden()
  await waitDone(page, /paket\.zip/)
  // the listing refreshes itself when the zip_edit task finishes
  await expect(entryRow(page, 'tambahan.txt')).toBeVisible({ timeout: 15_000 })
  await apiDeleteFinalTasks(page)

  await entryRow(page, 'tabel.csv').click()
  await page.getByTestId('archive-remove').click()
  await expect(page.getByTestId('archive-remove-dialog')).toBeVisible()
  await page.getByTestId('archive-remove-confirm').click()
  await waitDone(page, /paket\.zip/)
  await expect(entryRow(page, 'tabel.csv')).toHaveCount(0, { timeout: 15_000 })
  await expect(entryRow(page, 'readme.txt')).toBeVisible()
  await expect(entryRow(page, 'tambahan.txt')).toBeVisible()

  const host = join(e2eStorageRoot(), 'admin', 'jelajah', 'paket.zip')
  const listing = execFileSync('unzip', ['-l', host], { encoding: 'utf8' })
  expect(listing).toContain('tambahan.txt')
  expect(listing).toContain('readme.txt')
  expect(listing).toContain('docs/guide.txt')
  expect(listing).toContain('docs/deep/notes.md')
  expect(listing).not.toContain('tabel.csv')
  // integrity of the rewritten archive (raw-copied members keep their CRCs)
  execFileSync('unzip', ['-tq', host])
  expect(tmpWorkspaces(), 'zip edit must clean its workspace').toEqual([])
  await apiDeleteFinalTasks(page)
})

test('password zip: asked once in the overlay, kept for previews, never in the URL', async ({
  page,
}) => {
  const res = await apiPost(page, '/archives/compress', {
    paths: [`${DIR}/sumber/docs`],
    format: 'zip',
    name: 'kunci',
    destPath: DIR,
    password: 'sandi-rahasia',
  })
  expect(res.status(), await res.text()).toBe(202)
  await apiTaskDone(page, ((await res.json()) as { task: { id: string } }).task.id)
  await apiDeleteFinalTasks(page)

  await openBrowser(page, `${DIR}/kunci.zip`)
  await expect(page.getByTestId('archive-status-encrypted')).toContainText(/Terkunci/)
  await entryRow(page, 'docs').dblclick()
  // opening an encrypted member asks for the password
  await entryRow(page, 'docs/guide.txt').click()
  const prompt = page.getByTestId('archive-password')
  await expect(prompt).toBeVisible()
  await page.getByTestId('archive-password-input').fill('tebakan')
  await page.getByTestId('archive-password-submit').click()
  // wrong → 422 ARCHIVE_PASSWORD_WRONG → the prompt comes back with an error
  await expect(page.getByTestId('archive-password-error')).toBeVisible()
  await page.getByTestId('archive-password-input').fill('sandi-rahasia')
  await page.getByTestId('archive-password-submit').click()
  await expect(prompt).toBeHidden()
  await expect(page.getByTestId('archive-status-encrypted')).toContainText(/Terbuka/)

  // the preview now streams through a signed URL carrying a session id, not the password
  const signed = page.waitForRequest((r) => r.url().includes('/archives/entry-content'))
  await entryRow(page, 'docs/guide.txt').click()
  await expect(page.getByTestId('archive-preview-text')).toContainText('panduan lengkap arsip')
  const url = (await signed).url()
  expect(url).toContain('sess=')
  expect(url).not.toContain('sandi-rahasia')
  expect(page.url()).not.toContain('sandi-rahasia')
  expect(tmpWorkspaces()).toEqual([])
  await apiDeleteFinalTasks(page)
})
