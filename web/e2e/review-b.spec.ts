import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { apiCreateFile, apiMkdir, apiResetPrefs, csrfToken, loginAsAdmin } from './fixtures'

/** Review B (PROMPT Step R-B, checklist i): axe on /trash and /storage (the
 *  /files audit lives in details-view.spec.ts) and a keyboard-only rename +
 *  delete flow. */

test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/rb'
const ORIGIN = 'http://127.0.0.1:18090'
let seeded = false

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)

async function apiDelete(page: Page, paths: string[]) {
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/files/delete', {
    data: { paths },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
}

/** Leave the admin Recycle Bin empty for the specs that run after this one. */
async function apiEmptyTrash(page: Page) {
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/trash/empty', {
    data: { confirm: 'HAPUS' },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
}

async function expectNoSeriousViolations(page: Page) {
  const results = await new AxeBuilder({ page }).analyze()
  const bad = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  expect(bad.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual(
    [],
  )
}

/** Press Tab until the element with testId owns focus (bounded). */
async function tabTo(page: Page, testId: string, max = 12) {
  for (let i = 0; i < max; i++) {
    if (await page.getByTestId(testId).evaluate((el) => el === document.activeElement)) return
    await page.keyboard.press('Tab')
  }
  await expect(page.getByTestId(testId)).toBeFocused()
}

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'rb')
    for (const n of ['k1', 'k2', 'k3', 'k4']) await apiCreateFile(page.request, DIR, `${n}.txt`, n)
  }
})

test.afterEach(async ({ page }) => {
  await apiEmptyTrash(page)
})

test('axe: no serious or critical violations on /trash (with items and a selection)', async ({
  page,
}) => {
  await apiDelete(page, [`${DIR}/k3.txt`, `${DIR}/k4.txt`])
  await page.goto('/trash')
  await expect(page.getByTestId('trash-page')).toBeVisible()
  await expect(page.locator('[data-testid="trash-row"]')).toHaveCount(2)
  await page.locator('[data-testid="trash-row"]').first().click()
  await expect(page.getByTestId('trash-status-selected')).toContainText('1 dipilih')
  await expectNoSeriousViolations(page)
})

test('axe: no serious or critical violations on /storage', async ({ page }) => {
  await page.goto('/storage')
  await expect(page.getByTestId('storage-page')).toBeVisible()
  await expect(page.getByTestId('storage-summary')).toBeVisible()
  await expect(page.locator('[data-testid="storage-top-row"]').first()).toBeVisible()
  await expectNoSeriousViolations(page)
})

test('keyboard only: rename with F2 and delete with Delete + Enter', async ({ page }) => {
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  await expect(row(page, `${DIR}/k1.txt`)).toBeVisible()
  // reach the grid with Tab only, then select the first row with the keyboard
  await tabTo(page, 'details-grid', 40)
  await page.keyboard.press('Home')
  await expect(page.getByTestId('status-selected')).toContainText('1 dipilih')
  await page.keyboard.press('F2')
  const input = page.getByTestId('inline-rename-input')
  await expect(input).toBeFocused()
  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.type('k1-renamed.txt')
  await page.keyboard.press('Enter')
  await expect(row(page, `${DIR}/k1-renamed.txt`)).toBeVisible()
  await expect(page.getByTestId('details-grid')).toBeFocused()
  // re-select the renamed row (it sorts first), then delete it with the
  // keyboard; confirm with Tab + Enter
  await page.keyboard.press('Home')
  await expect(page.getByTestId('status-selected')).toContainText('1 dipilih')
  await page.keyboard.press('Delete')
  const confirm = page.getByTestId('delete-confirm')
  await expect(confirm).toBeVisible()
  await tabTo(page, 'delete-confirm-ok', 6)
  await page.keyboard.press('Enter')
  await expect(row(page, `${DIR}/k1-renamed.txt`)).toHaveCount(0)
  await expect(page.getByTestId('undo-toast').last()).toBeVisible() // rename + delete toasts
  await expect(page.getByTestId('details-grid')).toBeFocused()
})
