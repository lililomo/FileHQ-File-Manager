import AxeBuilder from '@axe-core/playwright'
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import {
  ADMIN_USERNAME,
  apiCreateFile,
  apiMkdir,
  apiDeletePermanent,
  apiResetPrefs,
  apiSetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

/**
 * Step 21 (SPEC §9.9, PRD G9): axe on every main screen — 0 serious/critical
 * violations in light and dark. WCAG 2.0/2.1/2.2 A+AA rules plus axe best
 * practices; the results of each page are printed so docs/A11Y.md can cite them.
 */
const DIR = '/aksesibilitas'
const ORIGIN = 'http://127.0.0.1:18090'
const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice']

test.describe.configure({ timeout: 90_000 })

let shareUrl = ''
let shareId = ''

async function apiPost(request: APIRequestContext, path: string, data: unknown) {
  const token = await csrfToken(request)
  return request.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

async function audit(page: Page, name: string) {
  // let entry transitions settle (Radix animations, toasts)
  await page.waitForTimeout(300)
  const results = await new AxeBuilder({ page }).withTags(TAGS).analyze()
  const bad = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  const minor = results.violations
    .filter((v) => !bad.includes(v))
    .map((v) => `${v.id}(${v.impact})`)
  console.log(
    `[a11y] ${name}: ${results.passes.length} rules pass, ${bad.length} serious/critical, other: ${minor.join(' ') || '-'}`,
  )
  expect(
    bad.map(
      (v) =>
        `${v.id} [${v.impact}] ${v.nodes.map((n) => `${n.target.join(' ')} ${n.html.slice(0, 160)}`).join(' | ')}`,
    ),
    name,
  ).toEqual([])
}

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage()
  await loginAsAdmin(page)
  await apiResetPrefs(page.request)
  const list = await page.request.get(`/api/v1/files/list?path=${encodeURIComponent(DIR)}`)
  if (list.status() !== 200) {
    await apiMkdir(page.request, '/', DIR.slice(1))
    await apiMkdir(page.request, DIR, 'dokumen')
    await apiCreateFile(page.request, DIR, 'catatan.md', '# Catatan\n\nIsi **tebal**.\n')
    await apiCreateFile(page.request, DIR, 'data.txt', 'baris 1\nbaris 2\n')
    const home = join(e2eStorageRoot(), ADMIN_USERNAME, DIR.slice(1))
    mkdirSync(home, { recursive: true })
    execFileSync('/usr/bin/vips', ['black', join(home, 'foto.png'), '96', '64'])
    // one item in the Recycle Bin
    await apiCreateFile(page.request, DIR, 'buang.txt', 'x')
    const del = await apiPost(page.request, '/files/delete', { paths: [`${DIR}/buang.txt`] })
    expect(del.ok(), await del.text()).toBeTruthy()
  }
  const res = await apiPost(page.request, '/shares', { path: DIR })
  expect(res.status(), await res.text()).toBe(201)
  const json = (await res.json()) as { share: { id: string; url: string } }
  shareId = json.share.id
  shareUrl = json.share.url
  await page.close()
})

test.afterAll(async ({ browser }) => {
  const page = await browser.newPage()
  await loginAsAdmin(page)
  // leave nothing for the specs that run after this one: view mode, folder, Recycle Bin
  await apiSetPrefs(page.request, { 'view.mode': null })
  // every share of the folder (a failed test restarts the worker and re-runs beforeAll)
  const list = await page.request.get(`/api/v1/shares/by-path?path=${encodeURIComponent(DIR)}`)
  const items = list.ok() ? ((await list.json()) as { items: { id: string }[] }).items : []
  const token = await csrfToken(page.request)
  for (const id of new Set([shareId, ...items.map((i) => i.id)].filter(Boolean)))
    await page.request.delete(`/api/v1/shares/${id}`, {
      headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
    })
  await apiDeletePermanent(page.request, [DIR])
  const empty = await apiPost(page.request, '/trash/empty', { confirm: 'HAPUS' })
  expect(empty.ok(), await empty.text()).toBeTruthy()
  await page.close()
})

for (const scheme of ['light', 'dark'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    test(`login (${scheme})`, async ({ page }) => {
      await page.goto('/login')
      await expect(page.getByLabel('Username')).toBeVisible()
      await audit(page, `/login ${scheme}`)
    })

    test(`files: Details and Large icons (${scheme})`, async ({ page }) => {
      await loginAsAdmin(page)
      await page.goto(`/files${DIR}`)
      await expect(page.locator(`[data-path="${DIR}/catatan.md"]`)).toBeVisible()
      await audit(page, `/files Details ${scheme}`)
      await page.getByTestId('toolbar-view').click()
      await page.getByTestId('view-mode-large').click()
      await expect(page.locator('[data-testid="browser"][data-view="large"]')).toBeVisible()
      await audit(page, `/files Large icons ${scheme}`)
      await page.getByTestId('toolbar-view').click()
      await page.getByTestId('view-mode-details').click()
      await expect(page.locator('[data-testid="browser"][data-view="details"]')).toBeVisible()
    })

    test(`viewer overlay (${scheme})`, async ({ page }) => {
      await loginAsAdmin(page)
      await page.goto(`/files${DIR}?view=${encodeURIComponent(`${DIR}/foto.png`)}`)
      await expect(page.getByTestId('viewer-overlay')).toBeVisible()
      await audit(page, `viewer overlay ${scheme}`)
    })

    test(`trash, search, shares (${scheme})`, async ({ page }) => {
      await loginAsAdmin(page)
      await page.goto('/trash')
      await expect(
        page.getByTestId('trash-grid').or(page.getByTestId('trash-empty')).first(),
      ).toBeVisible()
      await audit(page, `/trash ${scheme}`)
      await page.goto(`/search?q=catatan&path=${encodeURIComponent(DIR)}`)
      await expect(page.getByTestId('search-name-row').first()).toBeVisible()
      await audit(page, `/search ${scheme}`)
      await page.goto('/shares')
      await expect(page.getByTestId('my-share-row').first()).toBeVisible()
      await audit(page, `/shares ${scheme}`)
    })

    test(`editor (${scheme})`, async ({ page }) => {
      await loginAsAdmin(page)
      await page.request.put('/api/v1/me/editor-session', {
        data: { tabs: [], active: null },
        headers: { 'X-CSRF-Token': await csrfToken(page.request), Origin: ORIGIN },
      })
      await page.goto(`/editor?path=${encodeURIComponent(`${DIR}/data.txt`)}`)
      await expect(page.getByTestId('editor-status').first()).toBeVisible()
      await expect(page.locator('.monaco-editor').first()).toBeVisible()
      await audit(page, `/editor ${scheme}`)
    })

    test(`settings and admin users (${scheme})`, async ({ page }) => {
      await loginAsAdmin(page)
      await page.goto('/settings')
      await expect(page.getByTestId('preferences')).toBeVisible()
      await audit(page, `/settings ${scheme}`)
      await page.goto('/admin/users')
      await expect(page.getByTestId('admin-users')).toBeVisible()
      await expect(page.getByRole('row', { name: /admin/ }).first()).toBeVisible()
      await audit(page, `/admin/users ${scheme}`)
    })

    test(`public share page (${scheme})`, async ({ browser }) => {
      const ctx = await browser.newContext({ colorScheme: scheme })
      const guest = await ctx.newPage()
      try {
        await guest.goto(shareUrl)
        await expect(guest.getByTestId('public-browser')).toBeVisible()
        await expect(guest.getByText('catatan.md')).toBeVisible()
        await audit(guest, `/s/<token> ${scheme}`)
      } finally {
        await ctx.close()
      }
    })
  })
}
