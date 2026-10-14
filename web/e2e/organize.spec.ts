import { expect, test, type Page } from '@playwright/test'
import { rmSync } from 'node:fs'
import { join } from 'node:path'
import {
  apiCreateFile,
  apiMkdir,
  apiResetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/org'
const ORIGIN = 'http://127.0.0.1:18090'
let seeded = false

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)
const toast = (page: Page, text: string | RegExp) =>
  page.locator('[data-sonner-toaster]').getByText(text).last()

async function openDir(page: Page, path: string) {
  await page.goto(`/files${path}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
}

async function apiRename(page: Page, path: string, newName: string) {
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/files/rename', {
    data: { path, newName },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
}

async function apiJson<T>(page: Page, path: string): Promise<T> {
  const res = await page.request.get(`/api/v1${path}`)
  expect(res.status(), await res.text()).toBe(200)
  return (await res.json()) as T
}

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'org')
    await apiMkdir(page.request, DIR, 'proyek')
    for (const n of ['a', 'b', 'c', 'd'])
      await apiCreateFile(page.request, DIR, `${n}.txt`, n.repeat(3))
    await apiCreateFile(page.request, `${DIR}/proyek`, 'laporan.txt', 'laporan'.repeat(40))
    await apiCreateFile(page.request, DIR, 'hilang.txt', 'x')
  }
})

test('favourite via context menu, listed under Favorit, survives a rename', async ({ page }) => {
  await openDir(page, DIR)
  await row(page, `${DIR}/b.txt`).click({ button: 'right' })
  const menu = page.getByTestId('context-menu')
  await expect(menu).toBeVisible()
  await expect(menu.getByTestId('cm-favorite')).toBeVisible()
  await expect(menu.getByTestId('cm-unfavorite')).toHaveCount(0)
  await menu.getByTestId('cm-favorite').click()
  await expect(toast(page, '1 item ditambahkan ke Favorit')).toBeVisible()
  await expect(row(page, `${DIR}/b.txt`).locator('[data-badge="favorite"]')).toBeVisible()
  // the folder too
  await row(page, `${DIR}/proyek`).click({ button: 'right' })
  await page.getByTestId('context-menu').getByTestId('cm-favorite').click()
  await expect(toast(page, '1 item ditambahkan ke Favorit')).toBeVisible()

  await page.goto('/favorites')
  await expect(page.getByTestId('favorites-page')).toBeVisible()
  await expect(page.getByTestId('favorites-count')).toHaveText('2 favorit')
  await expect(page.locator('[data-testid="favorite-row"][data-path="/org/b.txt"]')).toContainText(
    '/org',
  )

  // rename through the API: the favourite follows the file (SPEC §5.7)
  await apiRename(page, `${DIR}/b.txt`, 'b2.txt')
  await page.reload()
  const renamed = page.locator('[data-testid="favorite-row"][data-path="/org/b2.txt"]')
  await expect(renamed).toBeVisible()
  await expect(renamed).not.toHaveAttribute('data-broken', 'true')
  await expect(page.locator('[data-testid="favorite-row"][data-path="/org/b.txt"]')).toHaveCount(0)

  // "Buka lokasi file" from Favorit navigates and selects the item
  await renamed.getByTestId('favorite-open-location').click()
  await expect(page).toHaveURL(/\/files\/org/)
  await expect(row(page, `${DIR}/b2.txt`)).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByTestId('status-selected')).toContainText('1 dipilih')
  // the context menu now offers "Hapus dari favorit"
  await row(page, `${DIR}/b2.txt`).click({ button: 'right' })
  await expect(page.getByTestId('context-menu').getByTestId('cm-unfavorite')).toBeVisible()
  await page.keyboard.press('Escape')
})

test('pinned item stays on top in every sort order', async ({ page }) => {
  await openDir(page, DIR)
  await row(page, `${DIR}/d.txt`).click({ button: 'right' })
  await page.getByTestId('context-menu').getByTestId('cm-pin').click()
  await expect(toast(page, '1 item disematkan')).toBeVisible()
  const rows = page.locator('[data-testid="details-body"] [role="row"]')
  await expect(rows.first()).toHaveAttribute('data-path', `${DIR}/d.txt`)
  await expect(rows.first().locator('[data-badge="pinned"]')).toBeVisible()
  // flip the sort direction via the header: still first
  await page.getByRole('columnheader', { name: /^Nama/ }).click()
  await expect(page.getByRole('columnheader', { name: /^Nama/ })).toHaveAttribute(
    'aria-sort',
    'descending',
  )
  await expect(rows.first()).toHaveAttribute('data-path', `${DIR}/d.txt`)
  // sort by size
  await page.getByRole('columnheader', { name: /^Ukuran/ }).click()
  await expect(rows.first()).toHaveAttribute('data-path', `${DIR}/d.txt`)
  // unpin
  await rows.first().click({ button: 'right' })
  await page.getByTestId('context-menu').getByTestId('cm-unpin').click()
  await expect(toast(page, '1 item dilepas sematannya')).toBeVisible()
  await expect(rows.first()).not.toHaveAttribute('data-path', `${DIR}/d.txt`)
})

test('inline note is saved and persists; 501 characters are refused', async ({ page }) => {
  await openDir(page, DIR)
  const cell = row(page, `${DIR}/a.txt`).getByTestId('note-cell')
  await cell.dblclick()
  const input = page.getByTestId('note-input')
  await expect(input).toBeVisible()
  await input.fill('catatan rapat')
  await input.press('Enter')
  await expect(toast(page, 'Catatan disimpan')).toBeVisible()
  await expect(row(page, `${DIR}/a.txt`).getByTestId('note-cell')).toHaveText('catatan rapat')
  await page.reload()
  await expect(row(page, `${DIR}/a.txt`).getByTestId('note-cell')).toHaveText('catatan rapat')
  // too long: the input refuses to save
  await row(page, `${DIR}/a.txt`).getByTestId('note-cell').dblclick()
  await page.getByTestId('note-input').fill('x'.repeat(501))
  await expect(page.getByTestId('note-input')).toHaveAttribute('aria-invalid', 'true')
  await page.getByTestId('note-input').press('Enter')
  await expect(page.getByTestId('note-input')).toBeVisible()
  await page.getByTestId('note-input').press('Escape')
  await expect(row(page, `${DIR}/a.txt`).getByTestId('note-cell')).toHaveText('catatan rapat')
  // the API also rejects 501 characters
  const token = await csrfToken(page.request)
  const res = await page.request.put('/api/v1/meta/notes', {
    data: { path: `${DIR}/a.txt`, note: 'y'.repeat(501) },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status()).toBe(400)
  expect(((await res.json()) as { error: { code: string } }).error.code).toBe('VALIDATION_FAILED')
})

test('colour tag via the submenu, badge dot and the tag filter', async ({ page }) => {
  await openDir(page, DIR)
  await row(page, `${DIR}/c.txt`).click()
  await row(page, `${DIR}/a.txt`).click({ modifiers: ['Control'] })
  await row(page, `${DIR}/a.txt`).click({ button: 'right' })
  const menu = page.getByTestId('context-menu')
  await expect(menu).toHaveAttribute('data-context', 'multi')
  await menu.getByTestId('cm-tagColor').hover()
  await page.getByTestId('cm-tag-red').click()
  await expect(toast(page, 'Tag Merah dipasang pada 2 item')).toBeVisible()
  await expect(row(page, `${DIR}/a.txt`).locator('[data-tag="red"]')).toBeVisible()
  await expect(row(page, `${DIR}/c.txt`).locator('[data-tag="red"]')).toBeVisible()
  await expect(row(page, `${DIR}/d.txt`).locator('[data-tag]')).toHaveCount(0)
  // filter: only red items
  await page.getByTestId('tag-filter').click()
  await page.getByTestId('tag-filter-red').click()
  await expect(page.getByTestId('tag-filter')).toHaveAttribute('data-tag', 'red')
  await expect(page.getByTestId('status-items')).toHaveText('2 item')
  await expect(row(page, `${DIR}/d.txt`)).toHaveCount(0)
  await page.getByTestId('tag-filter').click()
  await page.getByTestId('tag-filter-all').click()
  await expect(page.getByTestId('status-items')).toHaveText('6 item')
  // clear the tag on one item (left-click first: right-click inside a selection keeps it)
  await row(page, `${DIR}/c.txt`).click()
  await row(page, `${DIR}/c.txt`).click({ button: 'right' })
  await page.getByTestId('context-menu').getByTestId('cm-tagColor').hover()
  await page.getByTestId('cm-tag-none').click()
  await expect(toast(page, 'Tag dihapus dari 1 item')).toBeVisible()
  await expect(row(page, `${DIR}/c.txt`).locator('[data-tag]')).toHaveCount(0)
})

test('a favourite deleted on disk is flagged as broken, not an error', async ({ page }) => {
  await openDir(page, DIR)
  await row(page, `${DIR}/hilang.txt`).click({ button: 'right' })
  await page.getByTestId('context-menu').getByTestId('cm-favorite').click()
  await expect(toast(page, '1 item ditambahkan ke Favorit')).toBeVisible()
  rmSync(join(e2eStorageRoot(), 'admin', 'org', 'hilang.txt'))
  await page.goto('/favorites')
  const broken = page.locator('[data-testid="favorite-row"][data-path="/org/hilang.txt"]')
  await expect(broken).toBeVisible()
  await expect(broken).toHaveAttribute('data-broken', 'true')
  await expect(broken.getByTestId('favorite-broken')).toBeVisible()
  await broken.getByTestId('favorite-remove').click()
  await expect(toast(page, '1 item dihapus dari Favorit')).toBeVisible()
  await expect(broken).toHaveCount(0)
})

test('Recent lists an opened file with its location and reveals it', async ({ page }) => {
  await openDir(page, `${DIR}/proyek`)
  await row(page, `${DIR}/proyek/laporan.txt`).click()
  await page.keyboard.press('Space') // Quick Look = open
  await expect(page.getByTestId('quick-look')).toBeVisible()
  await page.keyboard.press('Escape')
  await page.goto('/recent')
  await expect(page.getByTestId('recent-page')).toBeVisible()
  const r = page.locator('[data-testid="recent-row"][data-path="/org/proyek/laporan.txt"]')
  await expect(r).toBeVisible()
  await expect(r).toContainText('/org/proyek')
  await r.getByTestId('recent-open-location').click()
  await expect(page).toHaveURL(/\/files\/org\/proyek/)
  await expect(row(page, `${DIR}/proyek/laporan.txt`)).toHaveAttribute('aria-selected', 'true')
  // the activity page shows the journal and can filter by action
  await page.goto('/activity')
  await expect(page.getByTestId('activity-page')).toBeVisible()
  await expect(page.locator('[data-testid="activity-row"]').first()).toBeVisible()
  await page.getByTestId('activity-action').click()
  await page.getByRole('option', { name: 'Buka' }).click()
  await expect(
    page.locator('[data-testid="activity-row"][data-action="open"]').first(),
  ).toBeVisible()
  await expect(page.locator('[data-testid="activity-row"]:not([data-action="open"])')).toHaveCount(
    0,
  )
})

test('Analisis penyimpanan shows a treemap whose total equals the quota usage', async ({
  page,
}) => {
  await page.goto('/storage')
  await expect(page.getByTestId('storage-page')).toBeVisible()
  await page.getByTestId('storage-recompute').click()
  await expect(page.getByTestId('treemap')).toBeVisible()
  await expect(page.locator('[data-testid="treemap-cell"]').first()).toBeVisible()
  const total = Number(await page.getByTestId('storage-total').getAttribute('data-value'))
  // same accounting rule as the quota walk (earlier specs seed files straight on
  // disk, so the incremental quota counter is compared through a fresh walk)
  const token = await csrfToken(page.request)
  const walk = await page.request.post('/api/v1/files/dir-size', {
    data: { path: '/' },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(walk.status(), await walk.text()).toBe(200)
  expect(total).toBe(((await walk.json()) as { size: number }).size)
  const quota = await apiJson<{ used: number; dirty: boolean }>(page, '/me/quota')
  if (!quota.dirty) expect(total).toBe(quota.used)
  // largest files sorted desc (other specs seed bigger files, so only the order is asserted)
  const rows = page.locator('[data-testid="storage-top-row"]')
  await expect(rows.first()).toBeVisible()
  const sizes = (await rows.evaluateAll((els) =>
    els.map((el) => Number(el.getAttribute('data-size'))),
  )) as number[]
  expect(sizes.length).toBeGreaterThan(0)
  for (let i = 1; i < sizes.length; i++) expect(sizes[i]!).toBeLessThanOrEqual(sizes[i - 1]!)
  const paths = (await rows.evaluateAll((els) =>
    els.map((el) => el.getAttribute('data-path')),
  )) as string[]
  expect(paths).toContain('/org/proyek/laporan.txt')
  // drill into a folder cell (its header strip; the nested level covers the middle)
  await page
    .locator('[data-testid="treemap-cell"][data-kind="dir"][data-depth="0"]')
    .first()
    .click({ position: { x: 12, y: 8 } })
  await expect(page.locator('[data-testid="storage-crumb"]')).toHaveCount(2)
  // open location of the largest file selects it in the browser
  const first = paths[0]!
  await rows.first().getByTestId('storage-top-open').click()
  const dir = first.slice(0, first.lastIndexOf('/')) || '/'
  await expect(page).toHaveURL(new RegExp(`/files${dir === '/' ? '' : dir}`))
  await expect(row(page, first)).toHaveAttribute('aria-selected', 'true')
})
