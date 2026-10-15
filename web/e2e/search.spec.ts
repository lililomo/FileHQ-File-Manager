import { expect, test, type Page } from '@playwright/test'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

/** Step 15 AC through the UI: 200 files with content → name search with an
 *  extension filter, content search (regex, whole word) listed per line,
 *  replace preview → apply → file changed → version restore through the API
 *  brings the old content back; Ctrl+Shift+F opens the content tab for the
 *  open folder. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/cari'
const ORIGIN = 'http://127.0.0.1:18090'
let seeded = false
let home = ''

function seed() {
  if (seeded) return
  home = join(e2eStorageRoot(), 'admin')
  const dir = join(home, 'cari')
  mkdirSync(join(dir, 'sub'), { recursive: true })
  for (let i = 0; i < 200; i++) {
    const n = String(i).padStart(3, '0')
    const ext = i < 150 ? 'txt' : 'md'
    const body = i % 3 === 0 ? `TODO item ${n}\nline two todos\n` : `nothing here ${n}\nline two\n`
    writeFileSync(join(i % 7 === 0 ? join(dir, 'sub') : dir, `note-${n}.${ext}`), body)
  }
  writeFileSync(join(dir, 'laporan.pdf'), '%PDF-1.4\n')
  seeded = true
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
  seed()
})

test('name search in a folder with an extension filter', async ({ page }) => {
  await page.goto(`/search?path=${encodeURIComponent(DIR)}`)
  await expect(page.getByTestId('search-page')).toBeVisible()
  await expect(page.getByTestId('search-path')).toHaveValue(DIR)
  await page.getByTestId('search-input').fill('note')
  await page.getByTestId('search-filters-toggle').click()
  await page.getByTestId('filter-ext').fill('txt')
  await page.getByTestId('search-submit').click()
  await expect(page.getByTestId('search-status')).toContainText('150')
  await expect(page.getByTestId('search-name-row').first()).toBeVisible()
  const first = page.getByTestId('search-name-row').first()
  await expect(first).toHaveAttribute('data-path', `${DIR}/sub/note-000.txt`)
  await expect(first).toContainText(`${DIR}/sub`)
  // the md files are gone with the filter; without it the pdf shows with types=pdf
  await page.getByTestId('filter-ext').fill('')
  await page.getByTestId('search-input').fill('')
  await page.getByTestId('filter-type').click()
  await page.getByRole('option', { name: 'PDF' }).click()
  await page.getByTestId('search-submit').click()
  await expect(page.getByTestId('search-status')).toContainText(/\b1\b/)
  await expect(page.getByTestId('search-name-row')).toHaveCount(1)
  await expect(page.getByTestId('search-name-row').first()).toHaveAttribute(
    'data-path',
    `${DIR}/laporan.pdf`,
  )
  // "Buka lokasi file" navigates to the folder and selects the item
  await page.getByTestId('search-open-location').first().click()
  await expect(page).toHaveURL(/\/files\/cari/)
  await expect(
    page.locator(`[data-testid="details-body"] [role="row"][data-path="${DIR}/laporan.pdf"]`),
  ).toHaveAttribute('aria-selected', 'true')
})

test('content search lists matches per line and Ctrl+Shift+F deep-links', async ({ page }) => {
  await page.goto('/files/cari')
  await expect(page.getByTestId('details-body')).toBeVisible()
  await page.keyboard.press('Control+Shift+F')
  await expect(page).toHaveURL(/\/search\?.*kind=content/)
  await expect(page.getByTestId('search-tab-content')).toHaveAttribute('data-state', 'active')
  await expect(page.getByTestId('search-path')).toHaveValue(DIR)
  await page.getByTestId('search-filters-toggle').click()
  await page.getByTestId('filter-regex').click()
  await page.getByTestId('filter-word').click()
  await page.getByTestId('search-input').fill('\\bTODO\\b')
  await page.getByTestId('search-submit').click()
  await expect(page.getByTestId('search-status')).toContainText(/67 file/)
  await expect(page.getByTestId('search-content-file')).toHaveCount(67)
  const file = page.locator(`[data-testid="search-content-file"][data-path="${DIR}/note-003.txt"]`)
  await expect(file).toBeVisible()
  const line = file.getByTestId('search-content-line')
  await expect(line).toHaveCount(1) // "todos" on line 2 is not a whole word
  await expect(line.first()).toHaveAttribute('data-line', '1')
  await expect(line.first().locator('mark')).toHaveText('TODO')
  // history keeps the query
  await page.getByTestId('search-history').click()
  await expect(page.getByTestId('search-history-list')).toContainText('\\bTODO\\b')
  await page.keyboard.press('Escape')
})

test('replace preview, apply and restore the pre-image version', async ({ page }) => {
  const target = join(home, 'cari', 'note-003.txt')
  const before = readFileSync(target, 'utf8')
  await page.goto(`/search?kind=content&path=${encodeURIComponent(DIR)}&q=TODO`)
  await expect(page.getByTestId('search-content-file').first()).toBeVisible()
  await page.getByTestId('search-replace-toggle').click()
  await page.getByTestId('replace-input').fill('DONE')
  await page.getByTestId('replace-preview').click()
  await expect(page.getByTestId('replace-summary')).toContainText('67', { timeout: 60_000 })
  const file = page.locator(`[data-testid="replace-file"][data-path="${DIR}/note-003.txt"]`)
  await expect(file.getByTestId('diff-before').first()).toContainText('TODO item 003')
  await expect(file.getByTestId('diff-after').first()).toContainText('DONE item 003')
  // apply to that one file only
  await page.getByRole('checkbox', { name: 'Pilih semua' }).click()
  await file.getByTestId('replace-file-check').click()
  await page.getByTestId('replace-apply').click()
  await expect(page.getByTestId('replace-result')).toBeVisible({ timeout: 60_000 })
  await expect(page.getByTestId('replace-result')).toContainText('2 penggantian di 1 file')
  expect(readFileSync(target, 'utf8')).toBe('DONE item 003\nline two DONEs\n')
  // restore the version through the API
  const list = await page.request.get(
    `/api/v1/versions?path=${encodeURIComponent(`${DIR}/note-003.txt`)}`,
  )
  expect(list.status(), await list.text()).toBe(200)
  const { items } = (await list.json()) as { items: { id: string; reason: string }[] }
  expect(items.length).toBe(1)
  expect(items[0]!.reason).toBe('replace_in_files')
  const restore = await apiPost(page, `/versions/${items[0]!.id}/restore`, {})
  expect(restore.status(), await restore.text()).toBe(200)
  expect(readFileSync(target, 'utf8')).toBe(before)
  // leave no finished tasks behind for the task-panel specs
  const token = await csrfToken(page.request)
  const res = await page.request.get('/api/v1/tasks?limit=50')
  const { items: tasks } = (await res.json()) as { items: { id: string; state: string }[] }
  for (const t of tasks) {
    if (t.state === 'queued' || t.state === 'running') continue
    await page.request.delete(`/api/v1/tasks/${t.id}`, {
      headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
    })
  }
})
