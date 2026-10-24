import { expect, test, type Page } from '@playwright/test'
import {
  ADMIN_NEW_PASSWORD,
  ADMIN_USERNAME,
  apiCreateFile,
  apiDeletePermanent,
  apiMkdir,
  apiResetPrefs,
  loginAsAdmin,
} from './fixtures'

/**
 * Step 21 (SPEC §9.9, WCAG 2.1.1): the core flow works with the keyboard only —
 * login → navigate → rename → delete → undo → upload through the file dialog.
 * No click/hover/mouse API is used; a page listener proves no pointer event
 * reached the document.
 */
const DIR = 'papan-ketik'

test.describe.configure({ mode: 'serial' })

test.beforeAll(async ({ browser }) => {
  // set-up through the API (not part of the flow): password changed, a folder with a file
  const page = await browser.newPage()
  await loginAsAdmin(page)
  await apiResetPrefs(page.request)
  const list = await page.request.get(`/api/v1/files/list?path=/${DIR}`)
  if (list.status() !== 200) {
    await apiMkdir(page.request, '/', DIR)
    await apiMkdir(page.request, `/${DIR}`, 'arsip')
    await apiCreateFile(page.request, `/${DIR}`, 'laporan-lama.txt', 'isi laporan\n')
  }
  await page.close()
})

async function focusedTestId(page: Page): Promise<string> {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null
    return el?.dataset.testid ?? el?.getAttribute('name') ?? el?.id ?? el?.tagName ?? ''
  })
}

/** Press Tab until `pred` holds for the focused element (fails after `max`). */
async function tabTo(page: Page, pred: (id: string) => boolean, max = 60) {
  for (let i = 0; i < max; i++) {
    if (pred(await focusedTestId(page))) return
    await page.keyboard.press('Tab')
  }
  throw new Error(`focus never reached the target (last: ${await focusedTestId(page)})`)
}

test('keyboard only: login, navigate, rename, delete, undo, upload', async ({ page, context }) => {
  await context.clearCookies()
  await page.goto('/login')
  await page.evaluate(() => {
    const w = window as unknown as { __pointer: string[] }
    w.__pointer = []
    for (const type of ['pointerdown', 'mousedown', 'click'])
      document.addEventListener(
        type,
        (e) => {
          // synthetic clicks from Enter/Space on buttons have detail 0 and no pointer
          if (type !== 'click' || (e as MouseEvent).detail > 0) w.__pointer.push(type)
        },
        true,
      )
  })

  // --- login ---
  await tabTo(page, (id) => id === 'username')
  await page.keyboard.type(ADMIN_USERNAME)
  await page.keyboard.press('Tab')
  await page.keyboard.type(ADMIN_NEW_PASSWORD)
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/files/)

  // --- navigate: skip link → main, address bar (Alt+D) → folder ---
  await tabTo(page, (id) => id === 'skip-link')
  await expect(page.getByText('Lompat ke konten')).toBeVisible()
  await page.keyboard.press('Enter')
  await expect(page.locator('#main')).toBeFocused()
  await page.keyboard.press('Alt+d')
  await expect(page.getByTestId('path-input')).toBeFocused()
  await page.keyboard.press('Control+a')
  await page.keyboard.type(`/${DIR}`)
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/files/${DIR}$`))
  await expect(page.locator(`[data-path="/${DIR}/laporan-lama.txt"]`)).toBeVisible()

  // focus the grid with Tab and pick the file by type-ahead
  await tabTo(page, (id) => id === 'details-grid')
  const ring = await page.evaluate(() => {
    const cs = getComputedStyle(document.activeElement as HTMLElement)
    return cs.boxShadow !== 'none' || cs.outlineStyle !== 'none'
  })
  expect(ring, 'focus ring on the grid').toBe(true)
  await page.keyboard.type('lap')
  await expect(page.locator(`[data-path="/${DIR}/laporan-lama.txt"]`)).toHaveAttribute(
    'aria-selected',
    'true',
  )

  // --- rename (F2) ---
  await page.keyboard.press('F2')
  const input = page.getByRole('textbox', { name: /nama/i })
  await expect(input).toBeFocused()
  await page.keyboard.press('Control+a')
  await page.keyboard.type('laporan-baru.txt')
  await page.keyboard.press('Enter')
  const renamed = page.locator(`[data-path="/${DIR}/laporan-baru.txt"]`)
  await expect(renamed).toBeVisible()
  await expect(page.getByTestId('details-grid')).toBeFocused()

  // --- delete (Delete → confirmation answered with the keyboard) → undo (Ctrl+Z) ---
  await expect(renamed).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('Delete')
  // the confirmation opens with the safe choice (Batal) focused; Tab reaches "Pindahkan"
  const move = page.getByRole('button', { name: 'Pindahkan', exact: true })
  await expect(move).toBeVisible()
  await expect(page.getByRole('button', { name: 'Batal' })).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(move).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(renamed).toHaveCount(0)
  await expect(
    page
      .getByRole('status')
      .filter({ hasText: /Recycle Bin|dipindahkan/i })
      .first(),
  ).toBeAttached()
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('Control+z')
  await expect(renamed).toBeVisible()

  // --- upload through the file dialog (Ctrl+U opens the picker) ---
  const chooser = page.waitForEvent('filechooser')
  await page.keyboard.press('Control+u')
  const fc = await chooser
  await fc.setFiles({
    name: 'dari-papan-ketik.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('halo\n'),
  })
  await expect(page.locator(`[data-path="/${DIR}/dari-papan-ketik.txt"]`)).toBeVisible({
    timeout: 20_000,
  })

  const pointer = await page.evaluate(
    () => (window as unknown as { __pointer: string[] }).__pointer,
  )
  expect(pointer, 'no mouse/pointer events reached the page').toEqual([])
})

test.afterAll(async ({ browser }) => {
  const page = await browser.newPage()
  await loginAsAdmin(page)
  await apiDeletePermanent(page.request, [`/${DIR}`])
  await apiResetPrefs(page.request)
  await page.close()
})
