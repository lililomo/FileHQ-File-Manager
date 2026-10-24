import { expect, test, type APIRequestContext, type Browser, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import {
  ADMIN_USERNAME,
  apiCreateFile,
  apiDeletePermanent,
  apiMkdir,
  apiResetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

/**
 * Step 21 (SPEC §9.9): three viewports — phone 375×812 (touch), tablet 820×1180
 * (touch), desktop 1440×900 — without horizontal scrolling on any main screen;
 * phone: drawer, compact toolbar, two-column Details, 44 px targets, 500 ms
 * long-press = context menu, selection bottom bar, usable viewer and editor;
 * tablet: 48 px icon sidebar and the preview panel as an overlay.
 */
const DIR = 'responsif'
const ORIGIN = 'http://127.0.0.1:18090'
const VIEWPORTS = {
  phone: { viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true },
  tablet: { viewport: { width: 820, height: 1180 }, hasTouch: true },
  desktop: { viewport: { width: 1440, height: 900 } },
} as const

test.describe.configure({ mode: 'serial', timeout: 120_000 })

let shareUrl = ''
let shareId = ''

async function apiPost(request: APIRequestContext, path: string, data: unknown) {
  const token = await csrfToken(request)
  return request.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

async function noHorizontalScroll(page: Page, what: string) {
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow, `${what}: document scrolls horizontally`).toBeLessThanOrEqual(0)
}

async function open(browser: Browser, kind: keyof typeof VIEWPORTS) {
  const ctx = await browser.newContext({ ...VIEWPORTS[kind], locale: 'id-ID' })
  const page = await ctx.newPage()
  await loginAsAdmin(page)
  return { ctx, page }
}

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage()
  await loginAsAdmin(page)
  await apiResetPrefs(page.request)
  if ((await page.request.get(`/api/v1/files/list?path=/${DIR}`)).status() !== 200) {
    await apiMkdir(page.request, '/', DIR)
    await apiMkdir(page.request, `/${DIR}`, 'proyek')
    await apiCreateFile(
      page.request,
      `/${DIR}`,
      'catatan-panjang-sekali-untuk-uji-layar.txt',
      'a\n',
    )
    await apiCreateFile(page.request, `/${DIR}`, 'kode.go', 'package main\n')
    const home = join(e2eStorageRoot(), ADMIN_USERNAME, DIR)
    mkdirSync(home, { recursive: true })
    execFileSync('/usr/bin/vips', ['black', join(home, 'foto.png'), '64', '48'])
  }
  const res = await apiPost(page.request, '/shares', { path: `/${DIR}` })
  expect(res.status(), await res.text()).toBe(201)
  const json = (await res.json()) as { share: { id: string; url: string } }
  shareId = json.share.id
  shareUrl = json.share.url
  await page.close()
})

test.afterAll(async ({ browser }) => {
  const page = await browser.newPage()
  await loginAsAdmin(page)
  if (shareId)
    await page.request.delete(`/api/v1/shares/${shareId}`, {
      headers: { 'X-CSRF-Token': await csrfToken(page.request), Origin: ORIGIN },
    })
  await apiDeletePermanent(page.request, [`/${DIR}`])
  await apiResetPrefs(page.request)
  await page.close()
})

for (const kind of Object.keys(VIEWPORTS) as (keyof typeof VIEWPORTS)[]) {
  test(`${kind}: no horizontal scroll on the main screens`, async ({ browser }) => {
    const { ctx, page } = await open(browser, kind)
    try {
      const pages: [string, () => Promise<void>][] = [
        [
          `/files/${DIR}`,
          () => expect(page.locator(`[data-path="/${DIR}/kode.go"]`)).toBeVisible(),
        ],
        ['/trash', () => expect(page.getByRole('heading').first()).toBeVisible()],
        ['/search?q=kode', () => expect(page.getByTestId('search-page')).toBeVisible()],
        ['/shares', () => expect(page.getByTestId('my-share-row').first()).toBeVisible()],
        ['/settings', () => expect(page.getByTestId('preferences')).toBeVisible()],
        ['/admin/users', () => expect(page.getByTestId('admin-users')).toBeVisible()],
        [
          `/files/${DIR}?view=${encodeURIComponent(`/${DIR}/foto.png`)}`,
          () => expect(page.getByTestId('viewer-overlay')).toBeVisible(),
        ],
        [
          `/editor?path=${encodeURIComponent(`/${DIR}/kode.go`)}`,
          () => expect(page.locator('.monaco-editor').first()).toBeVisible(),
        ],
      ]
      for (const [url, ready] of pages) {
        await page.goto(url)
        await ready()
        await page.waitForTimeout(200)
        await noHorizontalScroll(page, `${kind} ${url}`)
      }
      if (kind === 'desktop') {
        // review E m-17/m-21: wide tables fit at desktop width (no inner horizontal scroll)
        for (const url of ['/shares', '/admin/users']) {
          await page.goto(url)
          await expect(page.locator('table').first()).toBeVisible()
          const inner = await page
            .locator('table')
            .first()
            .evaluate((t) => {
              const box = t.parentElement!
              return box.scrollWidth - box.clientWidth
            })
          expect(inner, `${url}: table scrolls horizontally at 1440 px`).toBeLessThanOrEqual(0)
        }
      }
      const guest = await ctx.browser()!.newContext({ ...VIEWPORTS[kind] })
      const gp = await guest.newPage()
      await gp.goto(shareUrl)
      await expect(gp.getByTestId('public-browser')).toBeVisible()
      await noHorizontalScroll(gp, `${kind} /s/<token>`)
      await guest.close()
    } finally {
      await ctx.close()
    }
  })
}

test('phone: drawer, compact toolbar, two Details columns, 44 px targets', async ({ browser }) => {
  const { ctx, page } = await open(browser, 'phone')
  try {
    await page.goto(`/files/${DIR}`)
    await expect(page.locator(`[data-path="/${DIR}/kode.go"]`)).toBeVisible()
    // sidebar is a drawer
    await expect(page.getByRole('navigation', { name: 'Navigasi utama' })).toBeHidden()
    await page.getByRole('button', { name: 'Buka menu' }).tap()
    await expect(page.getByRole('navigation', { name: 'Navigasi utama' })).toBeVisible()
    await page.keyboard.press('Escape')
    // toolbar: [+] [Upload] … [⋯] only — the file operations live under ⋯
    await expect(page.getByTestId('toolbar-new')).toBeVisible()
    await expect(page.getByTestId('toolbar-more')).toBeVisible()
    await expect(page.getByTestId('toolbar-copy')).toHaveCount(0)
    // Details: name + size/modified stacked
    const headers = page.getByRole('columnheader')
    await expect(headers).toHaveCount(2)
    await expect(headers.nth(0)).toHaveText('Nama')
    await expect(headers.nth(1)).toHaveText('Ukuran / Diubah')
    // touch targets of the chrome (top bar, address bar, toolbar) are ≥ 44 × 44
    const small = await page.evaluate(() => {
      const out: string[] = []
      const scope = document.querySelectorAll(
        'header button, header a, [data-testid="browser"] > div:first-child button, [data-testid="browser"] [data-slot="button"]',
      )
      for (const el of scope) {
        const r = (el as HTMLElement).getBoundingClientRect()
        if (r.width === 0 || r.height === 0) continue // hidden
        if (r.width < 44 || r.height < 44)
          out.push(
            `${el.getAttribute('aria-label') ?? el.textContent?.trim()} ${Math.round(r.width)}×${Math.round(r.height)}`,
          )
      }
      return out
    })
    expect(small, 'buttons below 44 px on a phone').toEqual([])
  } finally {
    await ctx.close()
  }
})

test('phone: 500 ms long-press opens the context menu, tap selects with a bottom action bar', async ({
  browser,
}) => {
  const { ctx, page } = await open(browser, 'phone')
  try {
    await page.goto(`/files/${DIR}`)
    const row = page.locator(`[data-path="/${DIR}/kode.go"]`)
    await expect(row).toBeVisible()
    const box = (await row.boundingBox())!
    const x = box.x + box.width / 3
    const y = box.y + box.height / 2
    const cdp = await ctx.newCDPSession(page)
    // held 600 ms (Radix's own touch delay is 700 ms: the menu must come from the 500 ms handler)
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
    await page.waitForTimeout(600)
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    const menu = page.getByRole('menu')
    await expect(menu).toBeVisible()
    await expect(menu.getByRole('menuitem', { name: /^Rename/ })).toBeVisible()
    await noHorizontalScroll(page, 'context menu')
    await page.keyboard.press('Escape')
    await expect(menu).toBeHidden()
    // the long-press selected the row; the selection bar carries the actions
    const bar = page.getByTestId('selection-bar')
    await expect(bar).toBeVisible()
    await expect(bar).toContainText('1 dipilih')
    for (const id of [
      'selection-copy',
      'selection-rename',
      'selection-delete',
      'selection-more',
      'selection-clear',
    ]) {
      const b = (await page.getByTestId(id).boundingBox())!
      expect(Math.min(b.width, b.height), `${id} size`).toBeGreaterThanOrEqual(44)
    }
    await page.getByTestId('selection-clear').tap()
    await expect(bar).toBeHidden()
    await row.tap()
    await expect(bar).toBeVisible()
  } finally {
    await ctx.close()
  }
})

test('phone: viewer and editor are usable', async ({ browser }) => {
  const { ctx, page } = await open(browser, 'phone')
  try {
    await page.goto(`/files/${DIR}?view=${encodeURIComponent(`/${DIR}/foto.png`)}`)
    const overlay = page.getByTestId('viewer-overlay')
    await expect(overlay).toBeVisible()
    await expect(overlay.locator('img').first()).toBeVisible()
    await noHorizontalScroll(page, 'viewer')
    await page.keyboard.press('Escape')
    await expect(overlay).toBeHidden()
    await page.goto(`/editor?path=${encodeURIComponent(`/${DIR}/kode.go`)}`)
    await expect(page.getByTestId('editor-mobile-note')).toBeVisible()
    await expect(page.locator('.monaco-editor').first()).toBeVisible()
    await expect(page.getByTestId('editor-status').first()).toBeVisible()
    await noHorizontalScroll(page, 'editor')
  } finally {
    await ctx.close()
  }
})

test('tablet: 48 px icon sidebar that expands, preview panel as an overlay', async ({
  browser,
}) => {
  const { ctx, page } = await open(browser, 'tablet')
  try {
    await page.goto(`/files/${DIR}`)
    await expect(page.locator(`[data-path="/${DIR}/kode.go"]`)).toBeVisible()
    const aside = page.locator('aside').first()
    await expect.poll(async () => (await aside.boundingBox())!.width).toBeLessThanOrEqual(49)
    await page
      .getByRole('button', { name: /sidebar/i })
      .first()
      .tap()
    await expect.poll(async () => (await aside.boundingBox())!.width).toBeGreaterThan(200)
    await page
      .getByRole('button', { name: /sidebar/i })
      .first()
      .tap()
    await expect.poll(async () => (await aside.boundingBox())!.width).toBeLessThanOrEqual(49)
    await page.locator(`[data-path="/${DIR}/kode.go"]`).tap()
    await page.getByTestId('toolbar-preview').tap()
    const panel = page.getByTestId('preview-panel')
    await expect(panel).toBeVisible()
    await expect(panel).toHaveClass(/absolute/)
    await noHorizontalScroll(page, 'tablet preview overlay')
  } finally {
    await ctx.close()
  }
})
