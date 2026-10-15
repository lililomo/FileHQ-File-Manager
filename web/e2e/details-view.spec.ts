import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { apiCreateFile, apiMkdir, apiResetPrefs, e2eStorageRoot, loginAsAdmin } from './fixtures'

test.describe.configure({ mode: 'serial' })

const DIR = '/kolom'
let seeded = false

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  // let the shell flush its debounced `tabs.open` write before resetting the prefs
  await page.waitForTimeout(700)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'kolom')
    await apiMkdir(page.request, DIR, 'folder-a')
    await apiCreateFile(page.request, DIR, 'apel.txt', 'a'.repeat(10))
    await apiCreateFile(page.request, DIR, 'bola.md', 'b'.repeat(300))
    await apiCreateFile(page.request, DIR, 'foto1.jpg', 'c'.repeat(50))
    await apiCreateFile(page.request, DIR, 'foto2.png', 'd'.repeat(200))
    await apiCreateFile(page.request, DIR, 'zebra.csv', 'e'.repeat(100))
  }
})

async function openDir(page: Page) {
  await page.goto('/files/kolom')
  await expect(page.getByTestId('details-grid')).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText('6 item')
}

function rowNames(page: Page) {
  return page
    .locator('[data-testid="details-body"] [role="row"]')
    .evaluateAll((rows) => rows.map((r) => (r as HTMLElement).dataset.path?.split('/').pop() ?? ''))
}

test('sort: header click cycles asc → desc → default, Shift+click sets a secondary, persists', async ({
  page,
}) => {
  await openDir(page)
  const size = page.getByTestId('col-header-size')
  await expect(page.getByTestId('col-header-name')).toHaveAttribute('data-sort', 'asc')
  await size.click()
  await expect(size).toHaveAttribute('data-sort', 'asc')
  await expect
    .poll(() => rowNames(page))
    .toEqual(['folder-a', 'apel.txt', 'foto1.jpg', 'zebra.csv', 'foto2.png', 'bola.md'])
  await size.click()
  await expect(size).toHaveAttribute('data-sort', 'desc')
  await expect
    .poll(() => rowNames(page))
    .toEqual(['folder-a', 'bola.md', 'foto2.png', 'zebra.csv', 'foto1.jpg', 'apel.txt'])
  await page.getByTestId('col-header-type').click({ modifiers: ['Shift'] })
  await expect(page.getByTestId('col-header-type')).toHaveAttribute('data-sort', '2-asc')
  await expect(size).toHaveAttribute('data-sort', 'desc')
  // persisted on the server: reload keeps both sorts
  await page.reload()
  await expect(page.getByTestId('col-header-size')).toHaveAttribute('data-sort', 'desc')
  await expect(page.getByTestId('col-header-type')).toHaveAttribute('data-sort', '2-asc')
  // third click on the primary resets to the default (name asc) and clears the secondary
  await page.getByTestId('col-header-size').click()
  await expect(page.getByTestId('col-header-name')).toHaveAttribute('data-sort', 'asc')
  await expect(page.getByTestId('col-header-type')).not.toHaveAttribute('data-sort', /./)
})

test('columns: visibility, width and order survive reload and re-login (server prefs)', async ({
  page,
  context,
}) => {
  await openDir(page)
  await page.getByTestId('toolbar-columns').click()
  await page.getByTestId('column-toggle-ext').click()
  await page.getByTestId('column-toggle-note').click()
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('col-header-ext')).toBeVisible()
  await expect(page.getByTestId('col-header-note')).toHaveCount(0)
  // resize "Ukuran" by +60 px
  const handle = page.getByTestId('col-resize-size')
  const box = (await handle.boundingBox())!
  const before = (await page.getByTestId('col-header-size').boundingBox())!.width
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2 + 60, box.y + box.height / 2, { steps: 5 })
  await page.mouse.up()
  await expect
    .poll(async () => (await page.getByTestId('col-header-size').boundingBox())!.width)
    .toBeGreaterThan(before + 50)
  // reorder: drag "Tipe" header onto "Ukuran" → type comes before size
  await page.getByTestId('col-header-type').dragTo(page.getByTestId('col-header-size'))
  const order = async () =>
    page
      .locator('[data-testid="details-header"] [data-testid^="col-header-"]')
      .evaluateAll((els) =>
        els.map((e) => (e as HTMLElement).dataset.testid!.replace('col-header-', '')),
      )
  await expect.poll(order).toEqual(['name', 'type', 'size', 'mtime', 'ext'])
  // wait for the debounced PUT, then reload
  await page.waitForTimeout(800)
  await page.reload()
  await expect(page.getByTestId('col-header-ext')).toBeVisible()
  await expect(page.getByTestId('col-header-note')).toHaveCount(0)
  await expect.poll(order).toEqual(['name', 'type', 'size', 'mtime', 'ext'])
  await expect
    .poll(async () => (await page.getByTestId('col-header-size').boundingBox())!.width)
    .toBeGreaterThan(before + 50)
  // re-login: a fresh session gets the same preferences from the server
  await context.clearCookies()
  await loginAsAdmin(page)
  await openDir(page)
  await expect(page.getByTestId('col-header-ext')).toBeVisible()
  await expect.poll(order).toEqual(['name', 'type', 'size', 'mtime', 'ext'])
  await page.getByTestId('toolbar-columns').click()
  await page.getByTestId('columns-reset').click()
  await expect.poll(order).toEqual(['name', 'size', 'type', 'mtime', 'note'])
})

test('selection: click, Shift/Ctrl, keyboard range, Ctrl+A, Ctrl+I, Esc, checkboxes', async ({
  page,
}) => {
  await openDir(page)
  const grid = page.getByTestId('details-grid')
  const row = (name: string) => page.getByRole('row', { name: new RegExp(name) })
  const selected = () =>
    page.locator('[data-testid="details-body"] [role="row"][aria-selected="true"]').count()
  await row('apel').click()
  await expect.poll(selected).toBe(1)
  await row('foto2').click({ modifiers: ['Shift'] })
  await expect.poll(selected).toBe(4)
  await expect(page.getByTestId('status-bar')).toContainText('4 dipilih')
  await row('zebra').click({ modifiers: ['Control'] })
  await expect.poll(selected).toBe(5)
  await row('apel').click({ modifiers: ['Control'] })
  await expect.poll(selected).toBe(4)
  // keyboard: plain arrow moves the selection, Shift+arrow extends it
  await grid.focus()
  await page.keyboard.press('Home')
  await expect.poll(selected).toBe(1)
  await page.keyboard.press('Shift+ArrowDown')
  await page.keyboard.press('Shift+ArrowDown')
  await expect.poll(selected).toBe(3)
  await page.keyboard.press('Control+a')
  await expect.poll(selected).toBe(6)
  await expect(page.getByTestId('status-bar')).toContainText('6 dipilih')
  await expect(page.getByTestId('select-all')).toHaveAttribute('data-state', 'checked')
  await page.keyboard.press('Control+i')
  await expect.poll(selected).toBe(0)
  await row('bola').click()
  await page.keyboard.press('Control+i')
  await expect.poll(selected).toBe(5)
  await page.keyboard.press('Escape')
  await expect.poll(selected).toBe(0)
  // header checkbox selects / clears everything
  await page.getByTestId('select-all').click()
  await expect.poll(selected).toBe(6)
  await page.getByTestId('select-all').click()
  await expect.poll(selected).toBe(0)
})

test('Ctrl+A counts the whole folder, including pages not loaded yet', async ({ page }) => {
  const dir = join(e2eStorageRoot(), 'admin', 'banyak')
  mkdirSync(dir, { recursive: true })
  for (let i = 1; i <= 1200; i++)
    writeFileSync(join(dir, `item${String(i).padStart(4, '0')}.txt`), 'x')
  await page.goto('/files/banyak')
  await expect(page.getByTestId('status-items')).toHaveText('1.200 item')
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('Control+a')
  await expect(page.getByTestId('status-bar')).toContainText('1.200 dipilih')
  await expect(page.getByTestId('select-all')).toHaveAttribute('data-state', 'checked')
  // Alt+P preview panel summarises the selection
  await page.keyboard.press('Alt+p')
  await expect(page.getByTestId('preview-panel')).toContainText('1.200 item dipilih')
  await page.keyboard.press('Alt+p')
  await expect(page.getByTestId('preview-panel')).toHaveCount(0)
})

test('type-ahead, quick filter, Quick Look, Properties, preview panel', async ({ page }) => {
  await openDir(page)
  const grid = page.getByTestId('details-grid')
  await grid.focus()
  await page.keyboard.type('fo')
  const active = async () => {
    const id = await grid.getAttribute('aria-activedescendant')
    return page.locator(`#${id}`).getAttribute('data-path')
  }
  await expect.poll(active).toBe('/kolom/folder-a')
  await page.keyboard.type('t')
  await expect.poll(active).toBe('/kolom/foto1.jpg')
  // Space → Quick Look of the active item
  await page.keyboard.press('Space')
  const ql = page.getByTestId('quick-look')
  await expect(ql).toBeVisible()
  await expect(ql).toContainText('foto1.jpg')
  await page.keyboard.press('Escape')
  await expect(ql).toHaveCount(0)
  // Alt+Enter → Properties with stat facts
  await grid.focus()
  await page.keyboard.press('Alt+Enter')
  const props = page.getByTestId('properties')
  await expect(props).toBeVisible()
  await expect(props.getByTestId('item-facts')).toContainText('/kolom/foto1.jpg')
  await page.keyboard.press('Escape')
  // preview panel shows the single selected item
  await page.getByRole('row', { name: /bola\.md/ }).click()
  await page.getByTestId('toolbar-preview').click()
  await expect(page.getByTestId('preview-panel')).toContainText('bola.md')
  await expect(page.getByTestId('preview-panel').getByTestId('item-facts')).toContainText('300')
  await page.getByTestId('toolbar-preview').click()
  // quick filter narrows the loaded items client-side
  await page.keyboard.press('Control+f')
  const filter = page.getByTestId('quick-filter')
  await expect(filter).toBeFocused()
  await filter.fill('fot')
  await expect.poll(() => rowNames(page)).toEqual(['foto1.jpg', 'foto2.png'])
  await expect(page.getByTestId('status-items')).toHaveText('2 item')
  await page.keyboard.press('Escape')
  await expect(filter).toHaveValue('')
  await expect(page.getByTestId('status-items')).toHaveText('6 item')
})

test('folder tabs: open up to 10, 11th rejected, close/activate, restored after reload', async ({
  page,
}) => {
  await openDir(page)
  const tabs = page.locator('[data-testid="tab-bar"] [role="tab"]')
  await expect(tabs).toHaveCount(1)
  for (let i = 0; i < 9; i++) await page.getByTestId('tab-new').click()
  await expect(tabs).toHaveCount(10)
  await page.getByTestId('tab-new').click()
  await expect(page.getByText('Maksimal 10 tab terbuka')).toBeVisible()
  await expect(tabs).toHaveCount(10)
  // navigate inside the active (last) tab, then switch back to the first
  await page.getByRole('row', { name: /folder-a/ }).dblclick()
  await expect(page).toHaveURL(/\/files\/kolom\/folder-a$/)
  await expect(tabs.nth(9)).toHaveText(/folder-a/)
  await tabs.nth(0).click()
  await expect(page).toHaveURL(/\/files\/kolom$/)
  // Alt+1…9 selects a tab; Alt+W closes the active one
  await page.keyboard.press('Alt+9')
  await expect(tabs.nth(8)).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('Alt+w')
  await expect(tabs).toHaveCount(9)
  await page.waitForTimeout(800) // debounced prefs PUT
  await page.reload()
  await expect(page.locator('[data-testid="tab-bar"] [role="tab"]')).toHaveCount(9)
  // context menu: close others
  await tabs.nth(0).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Tutup tab lainnya' }).click()
  await expect(tabs).toHaveCount(1)
})

test('"?" lists the shortcuts and the command palette runs actions', async ({ page }) => {
  await openDir(page)
  await page.keyboard.press('Shift+?')
  const help = page.getByTestId('shortcut-help')
  await expect(help).toBeVisible()
  const count = await help.locator('[data-shortcut]').count()
  expect(count).toBeGreaterThanOrEqual(30)
  const combos = await help.locator('dd').allInnerTexts()
  for (const bad of ['Ctrl N', 'Ctrl T', 'Ctrl W', 'Ctrl L', 'Ctrl D', 'Ctrl Q']) {
    expect(combos.map((c) => c.replace(/\s+/g, ' ')).some((c) => c === bad)).toBe(false)
  }
  await page.keyboard.press('Escape')
  await expect(help).toHaveCount(0)
  // Ctrl+K → "Folder baru" action opens the dialog
  await page.keyboard.press('Control+k')
  const input = page.getByTestId('palette-input')
  await expect(input).toBeFocused()
  await input.fill('folder baru')
  await page.getByTestId('palette-action-newFolder').click()
  await expect(page.getByRole('dialog', { name: 'Folder baru' })).toBeVisible()
  await page.keyboard.press('Escape')
  // top bar search box opens the search page (Step 15); the palette offers "Cari file"
  await page.getByTestId('topbar-search').click()
  await expect(page).toHaveURL(/\/search/)
  await expect(page.getByTestId('search-input')).toBeFocused()
})

test('axe: no serious or critical violations on /files', async ({ page }) => {
  await openDir(page)
  await page.getByRole('row', { name: /apel/ }).click()
  const results = await new AxeBuilder({ page }).analyze()
  const bad = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  expect(bad.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual(
    [],
  )
})
