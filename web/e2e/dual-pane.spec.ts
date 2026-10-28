import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'
import {
  apiCreateFile,
  apiDeletePermanent,
  apiMkdir,
  apiResetPrefs,
  apiSetPrefs,
  csrfToken,
  loginAsAdmin,
} from './fixtures'

/** Step 23b — dual-pane (SPEC §9.3 varian dual-pane, §9.6 F6, §8.18 dualpane.*). */
test.describe.configure({ mode: 'serial', timeout: 120_000 })
test.use({ viewport: { width: 1440, height: 900 } })

const DIR = '/dp'
let seeded = false

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(700)
  if (!seeded) {
    seeded = true
    await apiResetPrefs(page.request)
    await apiSetPrefs(page.request, { 'view.mode': null, 'fileops.confirm_delete': null })
    await apiDeletePermanent(page.request, [DIR])
    await apiMkdir(page.request, '/', 'dp')
    await apiMkdir(page.request, DIR, 'kiri')
    await apiMkdir(page.request, DIR, 'kanan')
    for (const n of ['a', 'b', 'c', 'd'])
      await apiCreateFile(page.request, `${DIR}/kiri`, `${n}.txt`, n)
  }
})

test.afterAll(async ({ browser }) => {
  const page = await browser.newPage()
  await loginAsAdmin(page)
  await apiResetPrefs(page.request)
  await apiDeletePermanent(page.request, [DIR])
  // the keyboard test sends a file to the Recycle Bin: purge it so later specs
  // that count trash rows (review-b) start clean
  const trash = (await (await page.request.get('/api/v1/trash?limit=200')).json()) as {
    items: { id: string; originalPath: string }[]
  }
  const ids = trash.items.filter((it) => it.originalPath.startsWith(`${DIR}/`)).map((it) => it.id)
  if (ids.length > 0) {
    const res = await page.request.post('/api/v1/trash/purge', {
      data: { ids },
      headers: { 'X-CSRF-Token': await csrfToken(page.request), Origin: 'http://127.0.0.1:18090' },
    })
    expect(res.status(), await res.text()).toBe(200)
  }
  await page.close()
})

const left = (page: Page) => page.locator('[data-pane-host="left"]')
const right = (page: Page) => page.locator('[data-pane-host="right"]')
const row = (pane: Locator, path: string) =>
  pane.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)
const toast = (page: Page, text: string | RegExp) =>
  page.locator('[data-sonner-toaster]').getByText(text)

async function listNames(page: Page, path: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(path)}`)
  expect(res.ok()).toBeTruthy()
  const body = (await res.json()) as { items: { name: string }[] }
  return body.items.map((it) => it.name).sort()
}

async function storedPrefs(page: Page): Promise<Record<string, unknown>> {
  const me = (await (await page.request.get('/api/v1/auth/me')).json()) as {
    user: { prefs?: Record<string, unknown> }
  }
  return me.user.prefs ?? {}
}

/** Navigates the right pane with Alt+D (the address bar of the focused pane). */
async function rightGoTo(page: Page, path: string) {
  await right(page).getByTestId('status-bar').click()
  await page.keyboard.press('Alt+d')
  const input = right(page).getByTestId('path-input')
  await expect(input).toBeFocused()
  await input.fill(path)
  await input.press('Enter')
}

test('toggle via View menu and F6; the right pane navigates without touching the URL', async ({
  page,
}) => {
  await page.goto(`/files${DIR}/kiri`)
  await expect(left(page).getByTestId('status-items')).toHaveText('4 item')
  await expect(right(page)).toHaveCount(0)

  await page.getByTestId('toolbar-view').click()
  await page.getByTestId('view-dual-pane').click()
  await expect(page.getByRole('region', { name: 'Panel kanan' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Panel kiri' })).toBeVisible()
  await expect(page.getByTestId('toolbar')).toHaveCount(1) // shared toolbar
  await expect(left(page)).toHaveAttribute('data-pane-focused', 'true')

  // F6 reaches the page (Chromium) and toggles; the shortcut is in the "?" registry
  await row(left(page), `${DIR}/kiri/a.txt`).click()
  await page.keyboard.press('F6')
  await expect(right(page)).toHaveCount(0)
  await page.keyboard.press('F6')
  await expect(right(page)).toBeVisible()
  await left(page).getByTestId('status-bar').click() // "?" in the grid is type-ahead
  await page.keyboard.press('Shift+?')
  const help = page.getByTestId('shortcut-help')
  await expect(help.getByText('Dual-pane (dua panel) aktif/nonaktif')).toBeVisible()
  await expect(help.getByText('F6', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')

  // the right pane starts at "File Saya" and goes its own way; the URL stays the left pane's
  await rightGoTo(page, `${DIR}/kanan`)
  await expect(right(page).getByText('Folder ini kosong')).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`/files${DIR}/kiri$`))
  await expect(right(page)).toHaveAttribute('data-pane-focused', 'true')
  await expect(left(page).getByTestId('status-items')).toHaveText('4 item')
  // the right pane has its own tabs
  await expect(right(page).getByRole('tab')).toHaveCount(1)
  await expect(left(page).getByRole('tab')).toHaveCount(1)
  // both preferences reach the server (debounced merge patch)
  await expect
    .poll(async () => {
      const p = await storedPrefs(page)
      return [p['dualpane.enabled'], p['dualpane.right']]
    })
    .toEqual([true, { tabs: [{ path: `${DIR}/kanan` }], active: 0 }])
})

test('"Salin ke panel lain" copies; a second copy opens the conflict dialog', async ({ page }) => {
  await page.goto(`/files${DIR}/kiri`)
  await expect(right(page)).toBeVisible() // dualpane.enabled survived the reload
  await expect(right(page).getByText('Folder ini kosong')).toBeVisible() // dualpane.right too

  const a = row(left(page), `${DIR}/kiri/a.txt`)
  await a.click({ button: 'right' })
  await page.getByTestId('cm-copyToOtherPane').click()
  await expect(toast(page, '1 item disalin')).toBeVisible()
  await expect(row(right(page), `${DIR}/kanan/a.txt`)).toBeVisible()
  await expect(right(page).getByTestId('status-items')).toHaveText('1 item')
  await expect(left(page).getByTestId('status-items')).toHaveText('4 item')

  await a.click({ button: 'right' })
  await page.getByTestId('cm-copyToOtherPane').click()
  await expect(page.getByTestId('conflict-dialog')).toBeVisible()
  await page.getByTestId('conflict-cancel').click()
  await expect(page.getByTestId('conflict-dialog')).toBeHidden()
  // the dialog did not move the focus to the other pane
  await expect(left(page)).toHaveAttribute('data-pane-focused', 'true')

  // same folder on both sides → the action is shown but disabled
  await right(page).getByTestId('status-bar').click()
  await rightGoTo(page, `${DIR}/kiri`)
  await expect(right(page).getByTestId('status-items')).toHaveText('4 item')
  await a.click({ button: 'right' })
  await expect(page.getByTestId('cm-moveToOtherPane')).toHaveAttribute('data-disabled', '')
  await page.keyboard.press('Escape')
  await rightGoTo(page, `${DIR}/kanan`)
  await expect(right(page).getByTestId('status-items')).toHaveText('1 item')
  // the next test opens a new browser context: wait for the debounced pref patch
  await expect
    .poll(async () => (await storedPrefs(page))['dualpane.right'])
    .toEqual({ tabs: [{ path: `${DIR}/kanan` }], active: 0 })
})

test('drag from the left pane to the right pane moves (Ctrl copies); same folder is a no-op', async ({
  page,
}) => {
  await page.goto(`/files${DIR}/kiri`)
  await expect(row(right(page), `${DIR}/kanan/a.txt`)).toBeVisible()

  // synthetic drag with Ctrl: the badge says "Salin ke kanan", the drop copies
  const c = row(left(page), `${DIR}/kiri/c.txt`)
  const target = right(page).getByTestId('details-body')
  const dt = await page.evaluateHandle(() => new DataTransfer())
  await c.dispatchEvent('dragstart', { dataTransfer: dt })
  await target.dispatchEvent('dragenter', { dataTransfer: dt, ctrlKey: true })
  await target.dispatchEvent('dragover', { dataTransfer: dt, ctrlKey: true })
  await expect(right(page).getByTestId('pane-drop')).toHaveAttribute('data-op', 'copy')
  await expect(right(page).getByTestId('pane-drop')).toHaveText('Salin ke kanan')
  await target.dispatchEvent('dragover', { dataTransfer: dt, ctrlKey: false })
  await expect(right(page).getByTestId('pane-drop')).toHaveText('Pindahkan ke kanan')
  await target.dispatchEvent('drop', { dataTransfer: dt, ctrlKey: true })
  await c.dispatchEvent('dragend', { dataTransfer: dt })
  await expect(right(page).getByTestId('pane-drop')).toHaveCount(0)
  await expect(toast(page, '1 item disalin')).toBeVisible()
  await expect(row(right(page), `${DIR}/kanan/c.txt`)).toBeVisible()

  // a real mouse drag of b.txt onto the empty area of the right listing moves it
  await page.dragAndDrop(
    `[data-pane-host="left"] [role="row"][data-path="${DIR}/kiri/b.txt"]`,
    '[data-pane-host="right"] [data-testid="details-grid"]',
    { targetPosition: { x: 120, y: 300 } },
  )
  await expect(toast(page, '1 item dipindahkan')).toBeVisible()
  await expect(row(right(page), `${DIR}/kanan/b.txt`)).toBeVisible()
  await expect(row(left(page), `${DIR}/kiri/b.txt`)).toHaveCount(0)
  await expect.poll(() => listNames(page, `${DIR}/kanan`)).toEqual(['a.txt', 'b.txt', 'c.txt'])
  await expect.poll(() => listNames(page, `${DIR}/kiri`)).toEqual(['a.txt', 'c.txt', 'd.txt'])

  // a move from the right pane back to the left pane's address bar
  await page.dragAndDrop(
    `[data-pane-host="right"] [role="row"][data-path="${DIR}/kanan/b.txt"]`,
    '[data-pane-host="left"] [data-testid="address-bar"]',
    { targetPosition: { x: 8, y: 20 } },
  )
  await expect(toast(page, '1 item dipindahkan').first()).toBeVisible()
  await expect
    .poll(() => listNames(page, `${DIR}/kiri`))
    .toEqual(['a.txt', 'b.txt', 'c.txt', 'd.txt'])

  // dropping into the folder the item already is in does nothing (no dialog, no request)
  let requests = 0
  page.on('request', (r) => {
    if (/\/files\/(move|copy)$/.test(new URL(r.url()).pathname)) requests++
  })
  await page.dragAndDrop(
    `[data-pane-host="left"] [role="row"][data-path="${DIR}/kiri/d.txt"]`,
    '[data-pane-host="left"] [data-testid="details-grid"]',
    { targetPosition: { x: 120, y: 300 } },
  )
  await page.waitForTimeout(500)
  expect(requests).toBe(0)
  await expect(page.getByTestId('conflict-dialog')).toHaveCount(0)
  await expect(left(page).getByTestId('status-items')).toHaveText('4 item')
})

test('keyboard: Tab moves between the grids and Delete acts on the focused pane only', async ({
  page,
}) => {
  await page.goto(`/files${DIR}/kiri`)
  await expect(right(page).getByTestId('status-items')).toHaveText('2 item')
  await row(left(page), `${DIR}/kiri/d.txt`).click()
  const leftGrid = left(page).getByRole('grid')
  const rightGrid = right(page).getByRole('grid')
  await expect(leftGrid).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(rightGrid).toBeFocused()
  await expect(right(page)).toHaveAttribute('data-pane-focused', 'true')
  await expect(page.locator('#live-polite')).toContainText(/Panel kanan aktif/)
  await page.keyboard.press('Home') // selects a.txt in the right pane
  await expect(right(page).getByTestId('status-selected')).toContainText('1 dipilih')
  await page.keyboard.press('Delete')
  const confirm = page.getByTestId('delete-confirm')
  await expect(confirm).toContainText('a.txt')
  await page.getByTestId('delete-confirm-ok').click()
  await expect(row(right(page), `${DIR}/kanan/a.txt`)).toHaveCount(0)
  await expect.poll(() => listNames(page, `${DIR}/kanan`)).toEqual(['c.txt'])
  // the left pane's selection (d.txt) is untouched
  await expect
    .poll(() => listNames(page, `${DIR}/kiri`))
    .toEqual(['a.txt', 'b.txt', 'c.txt', 'd.txt'])
  await expect(left(page).getByTestId('status-selected')).toContainText('1 dipilih')
  await expect(rightGrid).toBeFocused() // focus returned to the right pane's grid
  await page.keyboard.press('Tab')
  await expect(leftGrid).toBeFocused()
  await expect(left(page)).toHaveAttribute('data-pane-focused', 'true')
})

test('axe: the dual-pane screen has no serious/critical violations (light and dark)', async ({
  page,
}) => {
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme })
    await page.goto(`/files${DIR}/kiri`)
    await expect(right(page).getByTestId('status-items')).toHaveText('1 item')
    await row(left(page), `${DIR}/kiri/a.txt`).click()
    await page.waitForTimeout(300)
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'])
      .analyze()
    const bad = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
    const minor = results.violations
      .filter((v) => !bad.includes(v))
      .map((v) => `${v.id}(${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)
    console.log(
      `[a11y] dual-pane ${scheme}: ${results.passes.length} rules pass, ${bad.length} serious/critical, other: ${minor.join(' ') || '-'}`,
    )
    expect(
      bad.map((v) => `${v.id} ${v.nodes.map((n) => n.target.join(' ')).join(' | ')}`),
      scheme,
    ).toEqual([])
  }
})

test('below 1280 px the UI falls back to one pane; the preference stays stored', async ({
  page,
}) => {
  await page.goto(`/files${DIR}/kiri`)
  await expect(right(page)).toBeVisible()
  await page.setViewportSize({ width: 1024, height: 768 })
  await expect(right(page)).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Panel kiri' })).toHaveCount(0)
  await page.getByTestId('toolbar-view').click()
  await expect(page.getByTestId('view-mode-details')).toBeVisible()
  await expect(page.getByTestId('view-dual-pane')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await page.keyboard.press('F6') // not registered below 1280 px
  await expect(right(page)).toHaveCount(0)
  expect((await storedPrefs(page))['dualpane.enabled']).toBe(true)
  expect((await storedPrefs(page))['dualpane.right']).toEqual({
    tabs: [{ path: `${DIR}/kanan` }],
    active: 0,
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(right(page)).toBeVisible()

  // switching it off from the View menu is stored as well
  await page.getByTestId('toolbar-view').click()
  await page.getByTestId('view-dual-pane').click()
  await expect(right(page)).toHaveCount(0)
  await expect.poll(async () => (await storedPrefs(page))['dualpane.enabled']).toBe(false)
})
