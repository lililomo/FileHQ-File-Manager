import { expect, test, type Page } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { apiMkdir, apiResetPrefs, csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

/** Step 14a AC through the UI: compress 2 files + 1 folder to zip and
 *  tar.gz, extract each into a new folder and compare the contents through
 *  the API; a wrong password fails the task with ARCHIVE_PASSWORD_WRONG and
 *  the dialog can be re-run with the right one; a zip-slip fixture (written
 *  straight into the storage root) is refused with a visible reason. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/arsip'
const ORIGIN = 'http://127.0.0.1:18090'
let seeded = false

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)
const toast = (page: Page, text: string | RegExp) =>
  page.locator('[data-sonner-toaster]').getByText(text)

async function apiPost(page: Page, path: string, data: unknown) {
  const token = await csrfToken(page.request)
  return page.request.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

async function apiList(page: Page, path: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(path)}`)
  expect(res.status(), await res.text()).toBe(200)
  const { items } = (await res.json()) as { items: { name: string }[] }
  return items.map((i) => i.name).sort()
}

async function apiSha256(page: Page, path: string): Promise<string> {
  const res = await apiPost(page, '/files/checksum', { path, algos: ['sha256'] })
  expect(res.status(), await res.text()).toBe(200)
  return ((await res.json()) as { sha256: string }).sha256
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

async function selectOption(page: Page, triggerTestId: string, optionText: string | RegExp) {
  await page.getByTestId(triggerTestId).click()
  await page.getByRole('option', { name: optionText }).click()
}

async function waitDone(page: Page, text: string | RegExp) {
  const panel = page.getByTestId('task-panel')
  await expect(panel).toBeVisible()
  const taskRow = panel.getByTestId('task-row').filter({ hasText: text }).first()
  await expect(taskRow).toHaveAttribute('data-state', 'done', { timeout: 60_000 })
  return taskRow
}

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(300)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'arsip')
    const root = join(e2eStorageRoot(), 'admin', 'arsip')
    mkdirSync(join(root, 'sumber', 'folder', 'dalam'), { recursive: true })
    writeFileSync(join(root, 'sumber', 'satu.txt'), 'isi satu\n'.repeat(500))
    writeFileSync(join(root, 'sumber', 'dua.bin'), Buffer.alloc(200_000, 7))
    writeFileSync(join(root, 'sumber', 'folder', 'dalam', 'tiga.md'), '# tiga\n')
    writeFileSync(join(root, 'sumber', 'folder', 'empat.csv'), 'a,b\n1,2\n')
  }
  await page.goto(`/files${DIR}/sumber`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
})

for (const format of ['zip', 'tar.gz'] as const) {
  test(`compress 2 files + folder to ${format} → extract to a new folder → identical`, async ({
    page,
  }) => {
    // select satu.txt, dua.bin and folder (Ctrl+click), then right click → Compress…
    await row(page, `${DIR}/sumber/dua.bin`).click()
    await row(page, `${DIR}/sumber/satu.txt`).click({ modifiers: ['Control'] })
    await row(page, `${DIR}/sumber/folder`).click({ modifiers: ['Control'] })
    await row(page, `${DIR}/sumber/folder`).click({ button: 'right' })
    await page.getByTestId('cm-compress').click()
    const dialog = page.getByTestId('compress-dialog')
    await expect(dialog).toBeVisible()
    // default name = parent folder + ext; destination = current folder
    await expect(page.getByTestId('compress-name')).toHaveValue(/^sumber\.zip$/)
    await expect(page.getByTestId('compress-dest')).toHaveValue(`${DIR}/sumber`)
    if (format !== 'zip') {
      await selectOption(page, 'compress-format', /^tar\.gz$/)
      await expect(page.getByTestId('compress-name')).toHaveValue('sumber.tar.gz')
    }
    await page.getByTestId('compress-name').fill(`paket.${format}`)
    await page.getByTestId('compress-dest').fill(DIR)
    await page.getByTestId('compress-submit').click()
    await expect(dialog).toBeHidden()
    await expect(toast(page, /dijalankan sebagai task/)).toBeVisible()
    await waitDone(page, `paket.${format}`)
    await expect(toast(page, /selesai/)).toBeVisible()

    // the archive shows up in /arsip; Extract via the context menu into "paket"
    await page.goto(`/files${DIR}`)
    const archiveRow = row(page, `${DIR}/paket.${format}`)
    await expect(archiveRow).toBeVisible({ timeout: 15_000 })
    await archiveRow.click({ button: 'right' })
    await page.getByTestId('cm-extract').click()
    const xdialog = page.getByTestId('extract-dialog')
    await expect(xdialog).toBeVisible()
    await expect(page.getByTestId('extract-dest-new')).toBeChecked()
    await page.getByTestId('extract-submit').click()
    await expect(xdialog).toBeHidden()
    await waitDone(page, `paket.${format}`)
    await expect(row(page, `${DIR}/paket`)).toBeVisible({ timeout: 15_000 })

    expect(await apiList(page, `${DIR}/paket`)).toEqual(['dua.bin', 'folder', 'satu.txt'])
    expect(await apiList(page, `${DIR}/paket/folder`)).toEqual(['dalam', 'empat.csv'])
    expect(await apiList(page, `${DIR}/paket/folder/dalam`)).toEqual(['tiga.md'])
    for (const f of ['dua.bin', 'satu.txt', 'folder/empat.csv', 'folder/dalam/tiga.md']) {
      expect(await apiSha256(page, `${DIR}/paket/${f}`), f).toBe(
        await apiSha256(page, `${DIR}/sumber/${f}`),
      )
    }
    // cleanup for the next format: move the extracted folder away
    const res = await apiPost(page, '/files/rename', {
      path: `${DIR}/paket`,
      newName: `paket-${format.replace('.', '-')}`,
    })
    expect(res.status(), await res.text()).toBe(200)
    await apiDeleteFinalTasks(page)
  })
}

test('password zip: wrong password fails with ARCHIVE_PASSWORD_WRONG, the right one extracts', async ({
  page,
}) => {
  await row(page, `${DIR}/sumber/satu.txt`).click()
  await row(page, `${DIR}/sumber/satu.txt`).click({ button: 'right' })
  await page.getByTestId('cm-compress').click()
  await page.getByTestId('compress-name').fill('rahasia.zip')
  await page.getByTestId('compress-dest').fill(DIR)
  await page.getByTestId('compress-password').fill('kunci-rahasia')
  await page.getByTestId('compress-confirm').fill('salah')
  await expect(page.getByTestId('compress-submit')).toBeDisabled()
  await page.getByTestId('compress-confirm').fill('kunci-rahasia')
  await page.getByTestId('compress-submit').click()
  await waitDone(page, 'rahasia.zip')

  await page.goto(`/files${DIR}`)
  const archiveRow = row(page, `${DIR}/rahasia.zip`)
  await expect(archiveRow).toBeVisible({ timeout: 15_000 })
  await archiveRow.click({ button: 'right' })
  await page.getByTestId('cm-extract').click()
  await page.getByTestId('extract-password').fill('tebakan')
  await page.getByTestId('extract-submit').click()
  const panel = page.getByTestId('task-panel')
  const failed = panel.getByTestId('task-row').filter({ hasText: 'rahasia.zip' }).first()
  await expect(failed).toHaveAttribute('data-state', 'failed', { timeout: 60_000 })
  await expect(failed.getByTestId('task-meta')).toContainText(/password|Password/)
  await expect(row(page, `${DIR}/rahasia`)).toHaveCount(0)

  // prompt again with the right password
  await archiveRow.click({ button: 'right' })
  await page.getByTestId('cm-extract').click()
  await page.getByTestId('extract-password').fill('kunci-rahasia')
  await page.getByTestId('extract-submit').click()
  await expect(
    panel.getByTestId('task-row').filter({ hasText: 'rahasia.zip' }).first(),
  ).toHaveAttribute('data-state', 'done', { timeout: 60_000 })
  await expect(row(page, `${DIR}/rahasia`)).toBeVisible({ timeout: 15_000 })
  expect(await apiList(page, `${DIR}/rahasia`)).toEqual(['satu.txt'])
  await apiDeleteFinalTasks(page)
})

test('zip-slip fixture is refused with a reason and nothing escapes', async ({ page }) => {
  // a zip with "../../evil.txt" written by hand (local header + central directory, stored)
  const name = Buffer.from('../../evil.txt')
  const body = Buffer.from('pwned')
  const crc = crc32(body)
  const local = Buffer.alloc(30)
  local.writeUInt32LE(0x04034b50, 0)
  local.writeUInt16LE(20, 4)
  local.writeUInt32LE(crc, 14)
  local.writeUInt32LE(body.length, 18)
  local.writeUInt32LE(body.length, 22)
  local.writeUInt16LE(name.length, 26)
  const central = Buffer.alloc(46)
  central.writeUInt32LE(0x02014b50, 0)
  central.writeUInt16LE(20, 4)
  central.writeUInt16LE(20, 6)
  central.writeUInt32LE(crc, 16)
  central.writeUInt32LE(body.length, 20)
  central.writeUInt32LE(body.length, 24)
  central.writeUInt16LE(name.length, 28)
  const cdOffset = local.length + name.length + body.length
  central.writeUInt32LE(0, 42)
  const eocd = Buffer.alloc(22)
  eocd.writeUInt32LE(0x06054b50, 0)
  eocd.writeUInt16LE(1, 8)
  eocd.writeUInt16LE(1, 10)
  eocd.writeUInt32LE(central.length + name.length, 12)
  eocd.writeUInt32LE(cdOffset, 16)
  const zip = Buffer.concat([local, name, body, central, name, eocd])
  const root = join(e2eStorageRoot(), 'admin', 'arsip')
  writeFileSync(join(root, 'slip.zip'), zip)

  await page.goto(`/files${DIR}`)
  const archiveRow = row(page, `${DIR}/slip.zip`)
  await expect(archiveRow).toBeVisible({ timeout: 15_000 })
  await archiveRow.click({ button: 'right' })
  await page.getByTestId('cm-extract').click()
  await page.getByTestId('extract-submit').click()
  const panel = page.getByTestId('task-panel')
  const failed = panel.getByTestId('task-row').filter({ hasText: 'slip.zip' }).first()
  await expect(failed).toHaveAttribute('data-state', 'failed', { timeout: 60_000 })
  await expect(failed.getByTestId('task-meta')).toContainText(/unsafe|tidak aman/i)
  const res = await page.request.get('/api/v1/files/stat?path=' + encodeURIComponent('/evil.txt'))
  expect(res.status()).toBe(404)
  await apiDeleteFinalTasks(page)
})

function crc32(buf: Buffer): number {
  let c = 0xffffffff
  for (const b of buf) {
    c ^= b
    for (let k = 0; k < 8; k++) c = c & 1 ? (c >>> 1) ^ 0xedb88320 : c >>> 1
  }
  return (c ^ 0xffffffff) >>> 0
}
