import { expect, test, type Page } from '@playwright/test'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { e2eStorageRoot, loginAsAdmin } from './fixtures'

/** Step 16a AC through the UI: Monaco loads only with the editor; three
 *  tabs survive a reload with the saved content; CRLF stays CRLF, EOL switch
 *  writes \r\n; GBK is read and saved back as UTF-8; drafts come back after
 *  a reload; tail mode for a 6 MB file and the binary notice; the 21st tab
 *  is refused; nginx.conf / .htaccess get Monarch highlighting. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

let home = ''
const GBK_TEXT =
  '这是一个用GBK编码保存的中文文本文件。它包含了几行中文内容，用于测试编辑器的编码检测功能。\n第二行：保存时转换为UTF-8。\n'
let seeded = false
function seed() {
  if (seeded) return
  home = join(e2eStorageRoot(), 'admin')
  const dir = join(home, 'edt')
  mkdirSync(join(dir, 'many'), { recursive: true })
  writeFileSync(join(dir, 'a.txt'), 'alpha\nbeta\n')
  writeFileSync(join(dir, 'b.md'), '# title\n\ntext\n')
  writeFileSync(join(dir, 'c.js'), 'const x = 1\n')
  writeFileSync(join(dir, 'crlf.txt'), 'one\r\ntwo\r\n')
  writeFileSync(join(dir, 'lf.txt'), 'x\ny\n')
  // GBK_TEXT encoded as GBK (bytes produced with Python's gbk codec)
  writeFileSync(
    join(dir, 'gbk.txt'),
    Buffer.from(
      'd5e2cac7d2bbb8f6d3c347424bb1e0c2ebb1a3b4e6b5c4d6d0cec4cec4b1becec4bcfea1a3cbfcb0fcbaacc1cbbcb8d0d0d6d0cec4c4dac8dda3acd3c3d3dab2e2cad4b1e0bcadc6f7b5c4b1e0c2ebbcecb2e2b9a6c4dca1a30ab5dab6fed0d0a3bab1a3b4e6cab1d7aabbbbceaa5554462d38a1a30a',
      'hex',
    ),
  )
  writeFileSync(join(dir, 'bin.dat'), Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0x00, 0x01, 0x02]))
  const line = 'log line with some padding text to make it longer ..........\n'
  writeFileSync(join(dir, 'big.log'), line.repeat(Math.ceil((6 * 1024 * 1024) / line.length)))
  writeFileSync(
    join(dir, 'nginx.conf'),
    'server {\n    listen 80;\n    server_name example.test;\n    location / { return 200; }\n}\n',
  )
  writeFileSync(join(dir, '.htaccess'), 'RewriteEngine On\nRewriteRule ^old$ /new [R=301,L]\n')
  for (let i = 0; i < 21; i++)
    writeFileSync(join(dir, 'many', `f${String(i).padStart(2, '0')}.txt`), `file ${i}\n`)
  seeded = true
}

const cspErrors: string[] = []
test.beforeEach(async ({ page }) => {
  page.on('console', (m) => {
    if (m.type() === 'error' && /Content Security Policy|worker/i.test(m.text()))
      cspErrors.push(m.text())
  })
  await loginAsAdmin(page)
  seed()
})

async function openEditor(page: Page, path: string) {
  await page.goto(`/editor?path=${encodeURIComponent(path)}`)
  await expect(page.getByTestId('editor-workspace')).toBeVisible()
  await expect(page.locator('.monaco-editor .view-lines')).toBeVisible({ timeout: 30_000 })
}

/** Focus Monaco by clicking into its text area (0.57 routes input through a
 *  hidden element; focusing the textarea directly does not activate it). */
async function focusEditor(page: Page) {
  await page.locator('.monaco-editor .view-lines').click()
}

test('Monaco is a separate chunk: the file browser never loads it', async ({ page }) => {
  const urls: string[] = []
  page.on('request', (r) => urls.push(r.url()))
  await page.goto('/files/edt')
  await expect(page.getByRole('grid')).toBeVisible()
  expect(urls.filter((u) => /editor\.api|\/vs-|monaco|\.worker-/.test(u))).toEqual([])
  // opening a text file shows the read-only viewer (Step 17b) — still no Monaco
  await page.getByRole('row', { name: /a\.txt/ }).dblclick()
  await expect(page.getByTestId('code-viewer')).toBeVisible()
  expect(urls.filter((u) => /editor\.api|\/vs-|monaco|\.worker-/.test(u))).toEqual([])
  // "Edit" brings the editor (modal) and its chunk
  await page.getByTestId('viewer-edit').click()
  await expect(page.getByTestId('editor-modal')).toBeVisible()
  await expect(page.locator('.monaco-editor .view-lines')).toBeVisible({ timeout: 30_000 })
  expect(urls.some((u) => /editor\.api|\/vs-/.test(u))).toBeTruthy()
  // maximize → route
  await page.getByTestId('btn-maximize').click()
  await expect(page).toHaveURL(/\/editor\?path=/)
  await expect(page.getByTestId('editor-tab')).toHaveCount(1)
})

test('three tabs, edit, Ctrl+S, reload: tabs restored and content saved', async ({ page }) => {
  await openEditor(page, '/edt/a.txt')
  // two more through Ctrl+P quick open
  for (const name of ['b.md', 'c.js']) {
    await focusEditor(page)
    await page.keyboard.press('Control+p')
    await expect(page.getByTestId('quick-open')).toBeVisible()
    await page.getByPlaceholder('Ketik nama file…').fill(name)
    await page
      .getByRole('option', { name: new RegExp(name) })
      .first()
      .click()
    await expect(page.getByTestId('editor-tab').filter({ hasText: name })).toBeVisible()
  }
  await expect(page.getByTestId('editor-tab')).toHaveCount(3)
  await page.getByTestId('editor-tab').filter({ hasText: 'a.txt' }).click()
  await expect(page.getByTestId('status-location')).toHaveText('/edt/a.txt')
  await focusEditor(page)
  await page.keyboard.press('Control+End')
  await page.keyboard.type('gamma')
  await expect(page.getByTestId('tab-dirty')).toHaveCount(1)
  await page.keyboard.press('Control+s')
  await expect(page.getByText('a.txt disimpan')).toBeVisible()
  await expect(page.getByTestId('tab-dirty')).toHaveCount(0)
  expect(readFileSync(join(home, 'edt', 'a.txt'), 'utf8')).toBe('alpha\nbeta\ngamma')
  // session persisted after 2 s, tabs restored after reload with the active file
  await page.waitForTimeout(2500)
  await page.goto('/editor')
  await expect(page.getByTestId('editor-tab')).toHaveCount(3)
  await expect(page.getByTestId('editor-tab').filter({ hasText: 'a.txt' })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(page.locator('.monaco-editor .view-lines')).toContainText('gamma')
  // close all for the next tests
  await page.getByTestId('editor-tab').first().click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Tutup semua' }).click()
  await expect(page.getByTestId('editor-empty')).toBeVisible()
})

test('CRLF is kept byte for byte; switching to CRLF writes \\r\\n', async ({ page }) => {
  await openEditor(page, '/edt/crlf.txt')
  await expect(page.getByTestId('status-eol')).toHaveText('CRLF')
  await focusEditor(page)
  await page.keyboard.press('Control+s') // not dirty → no-op save
  await expect(page.getByText('crlf.txt tidak berubah')).toBeVisible()
  expect(readFileSync(join(home, 'edt', 'crlf.txt'), 'latin1')).toBe('one\r\ntwo\r\n')
  await openEditor(page, '/edt/lf.txt')
  await expect(page.getByTestId('status-eol')).toHaveText('LF')
  await page.getByTestId('status-eol').click()
  await page.getByTestId('eol-crlf').click()
  await expect(page.getByTestId('status-eol')).toHaveText('CRLF')
  await page.getByTestId('btn-save').click()
  await expect(page.getByText('lf.txt disimpan')).toBeVisible()
  expect(readFileSync(join(home, 'edt', 'lf.txt'), 'latin1')).toBe('x\r\ny\r\n')
})

test('GBK is decoded and can be saved back as UTF-8', async ({ page }) => {
  await openEditor(page, '/edt/gbk.txt')
  await expect(page.getByTestId('status-encoding')).toHaveText('GBK')
  await expect(page.locator('.monaco-editor .view-lines')).toContainText('编码检测功能')
  await page.getByTestId('status-encoding').click()
  await page.getByTestId('encoding-save-with').click()
  await page.getByTestId('save-with-utf-8').click()
  await expect(page.getByText('gbk.txt disimpan')).toBeVisible()
  await expect(page.getByTestId('status-encoding')).toHaveText('UTF-8')
  expect(readFileSync(join(home, 'edt', 'gbk.txt'), 'utf8')).toBe(GBK_TEXT)
})

test('a draft survives a reload without saving', async ({ page }) => {
  await openEditor(page, '/edt/b.md')
  await focusEditor(page)
  await page.keyboard.press('Control+End')
  await page.keyboard.type('\ndraft line')
  await expect(page.getByTestId('tab-dirty')).toHaveCount(1)
  await page.waitForTimeout(2500) // draft autosave (2 s)
  await page.reload()
  await expect(page.getByTestId('banner-draft')).toBeVisible({ timeout: 30_000 })
  await page.getByTestId('draft-restore').click()
  await expect(page.locator('.monaco-editor .view-lines')).toContainText('draft line')
  await expect(page.getByTestId('tab-dirty')).toHaveCount(1)
  expect(readFileSync(join(home, 'edt', 'b.md'), 'utf8')).toBe('# title\n\ntext\n')
  // closing a dirty tab asks; "Jangan simpan" drops the draft
  await page.getByTestId('editor-tab').filter({ hasText: 'b.md' }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Tutup', exact: true }).click()
  await expect(page.getByTestId('close-dirty')).toBeVisible()
  await page.getByTestId('close-discard').click()
  await expect(page.getByTestId('editor-tab').filter({ hasText: 'b.md' })).toHaveCount(0)
})

test('tail mode for a 6 MB file, load more, binary notice', async ({ page }) => {
  await openEditor(page, '/edt/big.log')
  await expect(page.getByTestId('banner-tail')).toContainText('1.000 baris')
  await expect(page.getByTestId('status-readonly')).toBeVisible()
  await page.getByTestId('tail-more').click()
  await expect(page.getByTestId('banner-tail')).toContainText('2.000 baris')
  await page.goto('/editor?path=%2Fedt%2Fbin.dat')
  await expect(page.getByTestId('editor-notice')).toContainText('biner')
  await expect(page.getByRole('button', { name: 'Unduh' })).toBeVisible()
  // Ctrl+S on a read-only tab is refused
  await page.getByTestId('editor-tab').filter({ hasText: 'big.log' }).click()
  await focusEditor(page)
  await page.keyboard.press('Control+s')
  await expect(page.getByText('hanya-baca tidak bisa disimpan')).toBeVisible()
})

test('the 21st tab is refused; nginx.conf and .htaccess are highlighted', async ({ page }) => {
  await page.goto('/editor')
  await expect(page.getByTestId('editor-workspace')).toBeVisible()
  // close leftovers
  while ((await page.getByTestId('editor-tab').count()) > 0) {
    await page.getByTestId('editor-tab').first().click({ button: 'right' })
    await page.getByRole('menuitem', { name: 'Tutup semua' }).click()
    await page
      .getByTestId('close-discard')
      .click({ timeout: 2000 })
      .catch(() => {})
    await page.waitForTimeout(200)
  }
  const explorer = page.getByTestId('editor-explorer')
  if (!(await explorer.isVisible())) await page.getByTestId('toggle-explorer').click()
  await page.getByTestId('explorer-filter').fill('')
  await explorer.getByTestId('explorer-node').filter({ hasText: /^edt$/ }).click()
  await explorer
    .getByTestId('explorer-node')
    .filter({ hasText: /^many$/ })
    .click()
  for (let i = 0; i < 21; i++) {
    await explorer.locator(`[data-path="/edt/many/f${String(i).padStart(2, '0')}.txt"]`).click()
  }
  await expect(page.getByTestId('editor-tab')).toHaveCount(20)
  await expect(page.getByText('Maksimal 20 tab')).toBeVisible()
  await page.getByTestId('editor-tab').first().click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Tutup semua' }).click()
  await openEditor(page, '/edt/nginx.conf')
  await expect(page.getByTestId('status-language')).toHaveText('Nginx')
  const classes = await page
    .locator('.monaco-editor .view-lines span[class*="mtk"]')
    .evaluateAll((els) => new Set(els.map((e) => e.className)).size)
  expect(classes).toBeGreaterThan(1)
  await openEditor(page, '/edt/.htaccess')
  await expect(page.getByTestId('status-language')).toHaveText('Apache')
  const classes2 = await page
    .locator('.monaco-editor .view-lines span[class*="mtk"]')
    .evaluateAll((els) => new Set(els.map((e) => e.className)).size)
  expect(classes2).toBeGreaterThan(1)
  expect(cspErrors).toEqual([])
})
