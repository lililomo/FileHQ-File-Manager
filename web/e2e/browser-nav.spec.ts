import { expect, test } from '@playwright/test'
import { apiMkdir, loginAsAdmin } from './fixtures'

test.describe.configure({ mode: 'serial' })

// Timing gates (10 000 entries, warm navigation, scroll long tasks) live in perf.spec.ts
// since Step 21 and measure inside the page; this file only keeps a generous ceiling.
const LIMIT = { warmBack: 1500 }

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
})

test('navigation: click, breadcrumb, sibling menu, typed path, back/forward/up, F5, URL sync', async ({
  page,
}) => {
  await apiMkdir(page.request, '/', 'proyek')
  await apiMkdir(page.request, '/proyek', 'website')
  await apiMkdir(page.request, '/proyek/website', 'assets')
  await apiMkdir(page.request, '/proyek', 'dokumen')

  await page.goto('/files')
  const grid = page.getByTestId('details-grid')
  await expect(grid).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText('1 item')

  // double-click enters the folder and updates the URL
  await page.getByRole('row', { name: /proyek/ }).dblclick()
  await expect(page).toHaveURL(/\/files\/proyek$/)
  await expect(page.getByTestId('status-items')).toHaveText('2 item')

  // keyboard: ArrowDown + Enter opens "website"
  await grid.focus()
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/files\/proyek\/website$/)
  await expect(page.getByRole('row', { name: /assets/ })).toBeVisible()

  // breadcrumb link goes back up one level
  await page.getByRole('link', { name: 'proyek' }).click()
  await expect(page).toHaveURL(/\/files\/proyek$/)

  // sibling dropdown of the current segment lists folders of the parent
  await page.getByTestId('crumb-menu-/proyek').click()
  await expect(page.getByRole('menuitem', { name: 'proyek' })).toBeVisible()
  await page.keyboard.press('Escape')

  // typed path (Alt+D) navigates; invalid path is rejected inline
  await page.keyboard.press('Alt+d')
  const input = page.getByTestId('path-input')
  await expect(input).toBeFocused()
  await input.fill('/proyek/../x')
  await input.press('Enter')
  await expect(page.getByRole('alert')).toHaveText('Path tidak valid')
  await input.fill('/proyek/website/assets')
  await input.press('Enter')
  await expect(page).toHaveURL(/\/files\/proyek\/website\/assets$/)
  await expect(page.getByText('Folder ini kosong')).toBeVisible()

  // up, back, forward, keyboard variants
  await page.getByTestId('nav-up').click()
  await expect(page).toHaveURL(/\/files\/proyek\/website$/)
  await page.getByTestId('nav-back').click()
  await expect(page).toHaveURL(/\/files\/proyek\/website\/assets$/)
  await page.getByTestId('nav-forward').click()
  await expect(page).toHaveURL(/\/files\/proyek\/website$/)
  await page.keyboard.press('Alt+ArrowUp')
  await expect(page).toHaveURL(/\/files\/proyek$/)
  await page.keyboard.press('Alt+ArrowLeft')
  await expect(page).toHaveURL(/\/files\/proyek\/website$/)
  await page.keyboard.press('Alt+ArrowRight')
  await expect(page).toHaveURL(/\/files\/proyek$/)
  await page.keyboard.press('Backspace')
  await expect(page).toHaveURL(/\/files\/?$/)

  // F5 refreshes the listing in-app (no document reload)
  await page.evaluate(() => {
    ;(window as unknown as { __marker: number }).__marker = 42
  })
  await page.keyboard.press('F5')
  await expect(page.getByTestId('status-items')).toHaveText('1 item')
  expect(await page.evaluate(() => (window as unknown as { __marker: number }).__marker)).toBe(42)

  // unknown folder → friendly error state
  await page.goto('/files/tidak-ada')
  await expect(page.getByText('Folder tidak ditemukan')).toBeVisible()
  await page.getByRole('button', { name: 'Ke File Saya' }).click()
  await expect(page).toHaveURL(/\/files\/?$/)
})

test('new folder dialog creates a folder and refreshes the list', async ({ page }) => {
  await page.goto('/files/proyek')
  await page.getByTestId('toolbar-new').click()
  await page.getByTestId('toolbar-new-folder').click()
  await page.getByTestId('new-item-name').fill('laporan')
  await page.getByTestId('new-item-submit').click()
  await expect(page.getByRole('row', { name: /laporan/ })).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText('3 item')
  // duplicate name → inline error, dialog stays open
  await page.getByTestId('toolbar-new').click()
  await page.getByTestId('toolbar-new-folder').click()
  await page.getByTestId('new-item-name').fill('laporan')
  await page.getByTestId('new-item-submit').click()
  await expect(page.getByRole('alert')).toBeVisible()
  await page.keyboard.press('Escape')
})

test('warm cache: returning to a visited folder renders instantly, then revalidates', async ({
  page,
}) => {
  await page.goto('/files/proyek')
  await expect(page.getByRole('row', { name: /website/ })).toBeVisible()
  await page.getByRole('row', { name: /website/ }).dblclick()
  await expect(page).toHaveURL(/\/files\/proyek\/website$/)
  const t0 = Date.now()
  await page.getByTestId('nav-back').click()
  await expect(page.getByRole('row', { name: /website/ })).toBeVisible()
  const elapsed = Date.now() - t0
  console.log(`[perf] back-navigation with warm cache: ${elapsed} ms`)
  expect(elapsed).toBeLessThan(LIMIT.warmBack)
})

test('375 px: drawer sidebar, two columns, no horizontal scroll', async ({ page }) => {
  await apiMkdir(page.request, '/', 'mobil')
  await apiMkdir(page.request, '/mobil', 'website')
  await page.setViewportSize({ width: 375, height: 700 })
  await page.goto('/files/mobil')
  await expect(page.getByRole('row', { name: /website/ })).toBeVisible()
  const headers = page.getByRole('columnheader')
  await expect(headers).toHaveCount(2)
  await expect(headers.nth(0)).toHaveText('Nama')
  await expect(headers.nth(1)).toHaveText('Ukuran / Diubah')
  await expect(page.getByRole('navigation', { name: 'Navigasi utama' })).toBeHidden()
  await page.getByRole('button', { name: 'Buka menu' }).click()
  await expect(page.getByRole('navigation', { name: 'Navigasi utama' })).toBeVisible()
  await page.keyboard.press('Escape')
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(0)
})

test('keyboard: Tab reaches address bar, toolbar and grid with a visible focus ring', async ({
  page,
}) => {
  await apiMkdir(page.request, '/', 'kbd')
  await apiMkdir(page.request, '/kbd', 'anak')
  await page.goto('/files/kbd')
  await expect(page.getByTestId('details-grid')).toBeVisible()
  const seen = new Set<string>()
  // 7b added the folder tab bar, quick filter, sort/columns/preview buttons and the
  // select-all checkbox before the grid, so allow more Tab presses.
  for (let i = 0; i < 45; i++) {
    await page.keyboard.press('Tab')
    const id = await page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null
      return el?.dataset.testid ?? el?.getAttribute('aria-label') ?? el?.tagName ?? ''
    })
    seen.add(id)
    if (id === 'details-grid') break
  }
  expect(seen.has('nav-back')).toBe(true)
  expect(seen.has('toolbar-new')).toBe(true)
  expect(seen.has('details-grid')).toBe(true)
  const ring = await page.evaluate(() => {
    const el = document.activeElement as HTMLElement
    const cs = getComputedStyle(el)
    return cs.boxShadow !== 'none' || cs.outlineStyle !== 'none'
  })
  expect(ring).toBe(true)
})
