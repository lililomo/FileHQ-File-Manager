import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { apiCreateFile, apiDeletePermanent, apiMkdir, csrfToken, loginAsAdmin } from './fixtures'

/** Step 23c — duplicate finder (SPEC §7.1 duplicate_finder, §8.7 "Duplikat"). */
test.describe.configure({ mode: 'serial', timeout: 120_000 })
test.use({ viewport: { width: 1440, height: 900 } })

const DIR = '/dup23c'
const ORIGIN = 'http://127.0.0.1:18090'
const A = 'alpha duplicate content'
const B = 'bravo dup body'
const C = 'charlie-dup'
// 12 files: 3 groups (3 + 2 + 2 copies) and 5 unique files
const FILES: [string, string, string][] = [
  [DIR, 'a1.txt', A],
  [`${DIR}/sub`, 'a2.txt', A],
  [`${DIR}/sub`, 'a3.txt', A],
  [DIR, 'b1.txt', B],
  [`${DIR}/sub`, 'b2.txt', B],
  [DIR, 'c1.txt', C],
  [`${DIR}/sub`, 'c2.txt', C],
  [DIR, 'u1.txt', 'unique one'],
  [DIR, 'u2.txt', 'unique two!'],
  [DIR, 'u3.txt', 'unique three'],
  [`${DIR}/sub`, 'u4.txt', 'unique four'],
  [`${DIR}/sub`, 'u5.txt', 'unique five'],
]
const RECLAIMABLE = A.length * 2 + B.length + C.length
let seeded = false
let shareId = ''

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  if (!seeded) {
    seeded = true
    await apiDeletePermanent(page.request, [DIR])
    await apiMkdir(page.request, '/', DIR.slice(1))
    await apiMkdir(page.request, DIR, 'sub')
    for (const [dir, name, content] of FILES) {
      await apiCreateFile(page.request, dir, name, content)
      // distinct mtimes: a1/b1/c1 are the oldest copies → kept by default
      await page.waitForTimeout(15)
    }
  }
})

test.afterAll(async ({ browser }) => {
  const page = await browser.newPage()
  await loginAsAdmin(page)
  const headers = { 'X-CSRF-Token': await csrfToken(page.request), Origin: ORIGIN }
  if (shareId) await page.request.delete(`/api/v1/shares/${shareId}`, { headers })
  await apiDeletePermanent(page.request, [DIR])
  // the spec trashes copies: purge them so later specs counting trash rows start clean
  const trash = (await (await page.request.get('/api/v1/trash?limit=200')).json()) as {
    items: { id: string; originalPath: string }[]
  }
  const ids = trash.items.filter((it) => it.originalPath.startsWith(`${DIR}/`)).map((it) => it.id)
  if (ids.length > 0) {
    const res = await page.request.post('/api/v1/trash/purge', { data: { ids }, headers })
    expect(res.status(), await res.text()).toBe(200)
  }
  await page.close()
})

const toast = (page: Page, text: string | RegExp) =>
  page.locator('[data-sonner-toaster]').getByText(text)

async function listNames(page: Page, path: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(path)}`)
  expect(res.ok()).toBeTruthy()
  const body = (await res.json()) as { items: { name: string; isSymlink: boolean }[] }
  return body.items.map((it) => (it.isSymlink ? `${it.name}@` : it.name)).sort()
}

/** Opens the Duplikat tab, scopes it to DIR and runs a scan to completion. */
async function scan(page: Page, groups: number) {
  await page.goto('/storage?tab=duplicates')
  await expect(page.getByTestId('storage-tab-duplicates')).toHaveAttribute('aria-selected', 'true')
  await page.getByTestId('dup-pick').click()
  const dialog = page.getByTestId('dup-scope-dialog')
  await dialog.getByTestId(`picker-node-${DIR}`).click()
  await dialog.getByTestId('dup-scope-ok').click()
  await expect(page.getByTestId('dup-scope')).toHaveText(DIR)
  await page.getByTestId('dup-start').click()
  await expect(page.getByTestId('dup-groups')).toHaveAttribute('data-value', String(groups), {
    timeout: 30_000,
  })
  await expect(page.getByTestId('dup-summary')).toContainText(DIR)
}

test('T1 scan, delete copies to the Recycle Bin, undo, delete again', async ({ page }) => {
  await scan(page, 3)
  await expect(page.getByTestId('dup-reclaimable')).toHaveAttribute(
    'data-value',
    String(RECLAIMABLE),
  )
  await expect(page.getByTestId('dup-summary')).toContainText('Hasil disimpan 24 jam')
  // defaults: the oldest copy is kept, every other copy is marked
  const kept = page.locator('[data-testid="dup-row"][data-kept="true"]')
  await expect(kept).toHaveCount(3)
  await expect(kept.first()).toHaveAttribute('data-path', `${DIR}/a1.txt`)
  const del = page.getByTestId('dup-delete')
  await expect(del).toContainText('Hapus 4 item ke Recycle Bin')
  // changing the kept copy flips the checkboxes of that group
  const group = page.locator(`[data-testid="dup-group"]`).filter({ hasText: 'a1.txt' })
  await group.locator(`[data-path="${DIR}/sub/a2.txt"] [data-testid="dup-keep"]`).click()
  await expect(
    group.locator(`[data-path="${DIR}/a1.txt"] [data-testid="dup-remove"]`),
  ).toHaveAttribute('data-state', 'checked')
  await expect(
    group.locator(`[data-path="${DIR}/sub/a2.txt"] [data-testid="dup-remove"]`),
  ).toBeDisabled()
  await group.locator(`[data-path="${DIR}/a1.txt"] [data-testid="dup-keep"]`).click()

  await del.click()
  const confirm = page.getByTestId('dup-confirm')
  await expect(confirm).toBeVisible()
  await expect(page.getByTestId('dup-shared-warning')).toHaveCount(0)
  await page.getByTestId('dup-confirm-ok').click()
  await expect(toast(page, '4 item dipindahkan ke Recycle Bin')).toBeVisible()
  await expect(page.getByTestId('dup-groups')).toHaveAttribute('data-value', '0')
  expect(await listNames(page, `${DIR}/sub`)).toEqual(['u4.txt', 'u5.txt'])

  // undo from the toast: the copies are back, the report shows them again
  await page.getByTestId('undo-button').click()
  await expect(toast(page, /diurungkan/i)).toBeVisible()
  await expect
    .poll(() => listNames(page, `${DIR}/sub`))
    .toEqual(['a2.txt', 'a3.txt', 'b2.txt', 'c2.txt', 'u4.txt', 'u5.txt'])
  await expect(page.getByTestId('dup-groups')).toHaveAttribute('data-value', '3')

  // delete again, this time for good (still in the Recycle Bin)
  await page.getByTestId('dup-delete').click()
  await page.getByTestId('dup-confirm-ok').click()
  await expect(page.getByTestId('dup-groups')).toHaveAttribute('data-value', '0')
  await expect(page.getByText('Tidak ada file duplikat')).toBeVisible()
  expect(await listNames(page, DIR)).toEqual([
    'a1.txt',
    'b1.txt',
    'c1.txt',
    'sub',
    'u1.txt',
    'u2.txt',
    'u3.txt',
  ])
  expect(await listNames(page, `${DIR}/sub`)).toEqual(['u4.txt', 'u5.txt'])
})

test('T2 shared copies warn first; replace with symlinks and undo', async ({ page }) => {
  await apiMkdir(page.request, DIR, 'shared')
  await apiCreateFile(page.request, `${DIR}/shared`, 's1.txt', A)
  const res = await page.request.post('/api/v1/shares', {
    data: { path: `${DIR}/shared` },
    headers: { 'X-CSRF-Token': await csrfToken(page.request), Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(201)
  shareId = ((await res.json()) as { share: { id: string } }).share.id

  await scan(page, 1)
  const row = page.locator(`[data-testid="dup-row"][data-path="${DIR}/shared/s1.txt"]`)
  await expect(row.getByTestId('dup-shared')).toHaveText('Dibagikan')
  await page.getByTestId('dup-delete').click()
  const warning = page.getByTestId('dup-shared-warning')
  await expect(warning).toBeVisible()
  await expect(warning).toContainText(`${DIR}/shared/s1.txt`)
  await page.getByRole('button', { name: 'Batal', exact: true }).click()
  await expect(page.getByTestId('dup-confirm')).toHaveCount(0)
  expect(await listNames(page, `${DIR}/shared`)).toEqual(['s1.txt'])

  // replace with a symlink: the dialog explains the undo, the warning shows again
  await page.getByTestId('dup-symlink').click()
  await expect(page.getByTestId('dup-confirm')).toContainText('Urungkan memulihkan file asli')
  await expect(page.getByTestId('dup-shared-warning')).toBeVisible()
  await page.getByTestId('dup-confirm-ok').click()
  await expect(toast(page, '1 salinan diganti dengan symlink')).toBeVisible()
  expect(await listNames(page, `${DIR}/shared`)).toEqual(['s1.txt@'])
  await page.getByTestId('undo-button').click()
  await expect.poll(() => listNames(page, `${DIR}/shared`)).toEqual(['s1.txt'])
})

for (const scheme of ['light', 'dark'] as const) {
  test(`T3 axe on the Duplikat tab (${scheme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme })
    await page.goto('/storage?tab=duplicates')
    await expect(page.getByTestId('dup-groups')).toBeVisible()
    await expect(page.getByTestId('dup-group').first()).toBeVisible()
    await page.waitForTimeout(300)
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'])
      .analyze()
    const bad = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
    console.log(
      `[a11y] /storage?tab=duplicates ${scheme}: ${results.passes.length} rules pass, ${bad.length} serious/critical, other: ${
        results.violations
          .filter((v) => !bad.includes(v))
          .map((v) => `${v.id}(${v.impact})`)
          .join(' ') || '-'
      }`,
    )
    expect(bad.map((v) => `${v.id} ${v.nodes.map((n) => n.target.join(' ')).join(' | ')}`)).toEqual(
      [],
    )
  })
}
