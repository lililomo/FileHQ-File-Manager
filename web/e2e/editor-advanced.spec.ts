import { expect, test, type Browser, type Page } from '@playwright/test'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

/** Step 16b AC through the UI: two sessions edit the same file → conflict
 *  dialog with a Monaco diff → overwrite; three versions → compare → restore;
 *  find in files → click → cursor at line/col with the match selected;
 *  Markdown preview never executes scripts. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

let home = ''
let seeded = false
function seed() {
  if (seeded) return
  home = join(e2eStorageRoot(), 'admin')
  const dir = join(home, 'adv')
  mkdirSync(join(dir, 'fif'), { recursive: true })
  writeFileSync(join(dir, 'conf.txt'), 'base\n')
  writeFileSync(join(dir, 'hist.txt'), 'first\n')
  writeFileSync(join(dir, 'fif', 'a.txt'), 'alpha\nno hit here\n')
  writeFileSync(join(dir, 'fif', 'b.txt'), 'zero\nthe needle is here\nand a needle again\n')
  writeFileSync(join(dir, 'fif', 'c.md'), '# needle\n')
  writeFileSync(
    join(dir, 'doc.md'),
    [
      '# Heading',
      '',
      '<script>window.__xss = 1</script>',
      '',
      '<img src="x" onerror="window.__xss = 2">',
      '',
      '[bad](javascript:window.__xss=3)',
      '',
      '![ext](https://example.invalid/a.png)',
      '',
      'Some **bold** text.',
      '',
    ].join('\n'),
  )
  seeded = true
}

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  seed()
})

// editor.spec.ts (runs next) expects an empty tab session
test.afterEach(async ({ page }) => {
  const token = await csrfToken(page.request)
  await page.request.put('/api/v1/me/editor-session', {
    data: { tabs: [], activePath: null },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
})

async function openEditor(page: Page, path: string) {
  await page.goto(`/editor?path=${encodeURIComponent(path)}`)
  await expect(page.getByTestId('editor-workspace')).toBeVisible()
  await expect(page.locator('.monaco-editor .view-lines').first()).toBeVisible({ timeout: 30_000 })
  await expect(page.getByTestId('status-location')).toHaveText(path)
}

async function focusEditor(page: Page) {
  await page.locator('.monaco-editor .view-lines').first().click()
}

async function appendAndSave(page: Page, text: string, name: string) {
  await focusEditor(page)
  await page.keyboard.press('Control+End')
  await page.keyboard.type(text)
  await expect(page.getByTestId('tab-dirty')).toHaveCount(1)
  await page.keyboard.press('Control+s')
  await expect(page.getByTestId('tab-dirty')).toHaveCount(0)
  await expect(page.getByText(`${name} disimpan`).last()).toBeVisible()
}

async function secondSession(browser: Browser): Promise<Page> {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await loginAsAdmin(page)
  return page
}

async function versionsOf(page: Page, path: string) {
  const res = await page.request.get(`/api/v1/versions?path=${encodeURIComponent(path)}`)
  expect(res.ok()).toBeTruthy()
  return (await res.json()) as { items: { id: string; no: number; reason: string }[] }
}

test('two sessions: conflict → diff dialog → overwrite keeps mine and the server pre-image', async ({
  page,
  browser,
}) => {
  const other = await secondSession(browser)
  await openEditor(page, '/adv/conf.txt')
  await openEditor(other, '/adv/conf.txt')
  // session A saves first
  await appendAndSave(page, 'from A\n', 'conf.txt')
  // session B (stale etag) saves → 409 → conflict dialog with a Monaco diff
  await focusEditor(other)
  await other.keyboard.press('Control+End')
  await other.keyboard.type('from B\n')
  await other.keyboard.press('Control+s')
  const dialog = other.getByTestId('conflict-dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('.monaco-diff-editor')).toBeVisible({ timeout: 15_000 })
  await expect(dialog.locator('.monaco-diff-editor')).toContainText('from A')
  await dialog.getByTestId('conflict-overwrite').click()
  await expect(other.getByText('conf.txt disimpan').last()).toBeVisible()
  expect(readFileSync(join(home, 'adv', 'conf.txt'), 'utf8')).toBe('base\nfrom B\n')
  // versions: A's pre-image ("base") and the server pre-image B overwrote ("base + from A")
  const v = await versionsOf(page, '/adv/conf.txt')
  expect(v.items.map((x) => x.reason)).toEqual(['force_save', 'save'])
  await other.context().close()
})

test('version history: three versions → compare → restore brings the content back', async ({
  page,
}) => {
  await openEditor(page, '/adv/hist.txt')
  await appendAndSave(page, 'second\n', 'hist.txt')
  await appendAndSave(page, 'third\n', 'hist.txt')
  await appendAndSave(page, 'fourth\n', 'hist.txt')
  await expect(page.getByTestId('status-history')).toHaveText(/Riwayat \(3\)/)
  await page.getByTestId('status-history').click()
  const hist = page.getByTestId('version-history')
  await expect(hist).toBeVisible()
  await expect(hist.getByTestId('version-row')).toHaveCount(3)
  // compare v1 ("first") with the current content
  await hist.getByTestId('version-row').last().getByTestId('version-compare').click()
  const diff = page.getByTestId('diff-dialog')
  await expect(diff).toBeVisible()
  await expect(diff.getByTestId('diff-left-label')).toContainText('Versi 1')
  await expect(diff.locator('.monaco-diff-editor')).toContainText('fourth', { timeout: 15_000 })
  await diff.getByTestId('diff-inline').click()
  await diff.getByRole('button', { name: 'Tutup' }).click()
  await expect(diff).toBeHidden()
  // restore v1
  await hist.getByTestId('version-row').last().getByTestId('version-restore').click()
  await page.getByTestId('version-confirm-ok').click()
  await expect(page.getByText(/dipulihkan ke versi 1/)).toBeVisible()
  await expect(page.locator('.monaco-editor .view-lines').first()).not.toContainText('fourth')
  expect(readFileSync(join(home, 'adv', 'hist.txt'), 'utf8')).toBe('first\n')
  const v = await versionsOf(page, '/adv/hist.txt')
  expect(v.items.length).toBe(4)
  expect(v.items[0]!.reason).toBe('restore')
  await expect(hist.getByTestId('version-row')).toHaveCount(4)
  await page.keyboard.press('Escape')
})

test('find in files: results per file → click places the cursor at line and column', async ({
  page,
}) => {
  await openEditor(page, '/adv/fif/a.txt')
  await focusEditor(page)
  await page.keyboard.press('Control+Shift+f')
  const panel = page.getByTestId('find-in-files')
  await expect(panel).toBeVisible()
  await expect(panel.getByTestId('find-folder')).toHaveValue('/adv/fif')
  await panel.getByTestId('find-query').fill('needle')
  await panel.getByTestId('find-run').click()
  await expect(panel.getByTestId('find-summary')).toContainText('3 kecocokan di 2 file')
  await expect(panel.getByTestId('find-file')).toHaveCount(2)
  const match = panel.getByTestId('find-match').filter({ hasText: 'needle again' })
  await expect(match).toHaveAttribute('data-line', '3')
  await match.click()
  await expect(page.getByTestId('editor-tab').filter({ hasText: 'b.txt' })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(page.getByTestId('status-position')).toHaveText('Ln 3, Col 7')
  // the match is selected
  await expect(page.locator('.monaco-editor .selected-text').first()).toBeVisible()
  // replace from here reuses the Step 15 panel
  await panel.getByTestId('find-replace').click()
  await expect(page.getByTestId('find-replace-dialog')).toBeVisible()
  await page.keyboard.press('Escape')
})

test('Markdown preview is sanitised: no script, no event handler, no javascript: link', async ({
  page,
}) => {
  await openEditor(page, '/adv/doc.md')
  await page.getByTestId('btn-preview').click()
  const preview = page.getByTestId('markdown-preview')
  await expect(preview).toBeVisible()
  await expect(preview.locator('h1')).toHaveText('Heading')
  await expect(preview.locator('strong')).toHaveText('bold')
  expect(await preview.locator('script').count()).toBe(0)
  expect(await preview.locator('img[onerror]').count()).toBe(0)
  expect(await preview.locator('a[href^="javascript"]').count()).toBe(0)
  // the external image became a placeholder, the local one never resolves to a foreign origin
  await expect(preview.locator('[data-placeholder="image"]')).toHaveCount(2)
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined()
  // formatting an invalid JSON leaves the content untouched
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/files/create', {
    data: { path: '/adv', name: 'bad.json', content: '{"a": 1,}' },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
  expect(res.status()).toBe(201)
  await openEditor(page, '/adv/bad.json')
  await focusEditor(page)
  await page.keyboard.press('Shift+Alt+f')
  await expect(page.getByText(/JSON tidak valid/)).toBeVisible()
  expect(readFileSync(join(home, 'adv', 'bad.json'), 'utf8')).toBe('{"a": 1,}')
  await expect(page.getByTestId('tab-dirty')).toHaveCount(0)
})
