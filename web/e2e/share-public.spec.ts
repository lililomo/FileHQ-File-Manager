import AxeBuilder from '@axe-core/playwright'
import { expect, test, type APIRequestContext, type Browser, type Page } from '@playwright/test'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { apiCreateFile, apiMkdir, apiResetPrefs, csrfToken, loginAsAdmin } from './fixtures'

/** Step 18a AC through the real binary: a password-protected folder link
 *  (1 day, 2 downloads) opened in a context without a session → wrong
 *  password ×3 → right → browse → download → zip → limit → 410; a
 *  preview-only file link shows no download button; disable → gone; rename
 *  keeps the link; delete to the Recycle Bin disables it; a file request
 *  accepts a guest upload that lands in the owner's folder; the badge shows
 *  in the owner's listing; public page headers and axe. */
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/berbagi'
const ORIGIN = 'http://127.0.0.1:18090'
let seeded = false
let work = ''

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)

async function apiPost(request: APIRequestContext, path: string, data: unknown) {
  const token = await csrfToken(request)
  return request.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

async function createShare(request: APIRequestContext, body: Record<string, unknown>) {
  const res = await apiPost(request, '/shares', body)
  expect(res.status(), await res.text()).toBe(201)
  const json = (await res.json()) as { share: { id: string }; url: string }
  return { id: json.share.id, url: json.url, token: json.url.slice(json.url.lastIndexOf('/') + 1) }
}

async function guestContext(browser: Browser) {
  const ctx = await browser.newContext()
  return { ctx, page: await ctx.newPage() }
}

test.beforeAll(() => {
  work = mkdtempSync(join(tmpdir(), 'fm-e2e-share-'))
})
test.afterAll(() => {
  rmSync(work, { recursive: true, force: true })
})

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'berbagi')
    await apiCreateFile(page.request, DIR, 'ringkasan.txt', 'isi ringkasan untuk tamu')
    await apiCreateFile(page.request, DIR, 'kedua.txt', 'file kedua')
    await apiMkdir(page.request, DIR, 'sub')
    await apiCreateFile(page.request, `${DIR}/sub`, 'dalam.txt', 'di dalam sub')
    await apiMkdir(page.request, '/', 'drop')
  }
})

test('share dialog creates a password link; the guest unlocks, browses, downloads until the limit', async ({
  page,
  browser,
}) => {
  await page.goto(`/files/`)
  await expect(row(page, DIR)).toBeVisible()
  // context menu → Share → dialog
  await row(page, DIR).click({ button: 'right' })
  await page.getByRole('menuitem', { name: /^Share$/ }).click()
  const dialog = page.getByTestId('share-dialog')
  await expect(dialog).toBeVisible()
  await expect(page.getByTestId('share-none')).toBeVisible()
  await page.getByTestId('share-password').fill('rahasia-tamu')
  await page.getByTestId('share-expiry').click()
  await page.getByRole('option', { name: '1 hari' }).click()
  await page.getByTestId('share-max-downloads').fill('2')
  await page.getByTestId('share-create').click()
  const link = page.getByTestId('share-link')
  await expect(link).toBeVisible()
  const url = await link.inputValue()
  expect(url).toMatch(/\/s\/[A-Za-z0-9_-]{43}$/)
  await page.getByTestId('share-qr-toggle').click()
  await expect(page.getByTestId('share-qr')).toBeVisible()
  await page.keyboard.press('Escape')
  // the badge shows in the listing
  await expect(row(page, DIR).locator('[data-badge="shared"]')).toBeVisible()

  // --- guest -----------------------------------------------------------------
  const { ctx, page: guest } = await guestContext(browser)
  try {
    const res = await guest.goto(url)
    expect(res?.headers()['referrer-policy']).toBe('no-referrer')
    expect(res?.headers()['x-robots-tag']).toContain('noindex')
    const form = guest.getByTestId('public-password-form')
    await expect(form).toBeVisible()
    for (let i = 0; i < 3; i++) {
      await guest.getByTestId('public-password').fill('salah')
      await guest.getByTestId('public-unlock').click()
      await expect(guest.getByRole('alert')).toHaveText('Password salah')
    }
    await guest.getByTestId('public-password').fill('rahasia-tamu')
    await guest.getByTestId('public-unlock').click()
    await expect(guest.getByTestId('public-browser')).toBeVisible()
    await expect(guest.getByTestId('public-name')).toHaveText('berbagi')
    await expect(guest.getByTestId('public-owner')).toContainText(/admin/i)
    // nothing above the share: breadcrumb starts at the folder
    await expect(guest.getByTestId('public-crumb-current')).toHaveText('berbagi')
    await expect(guest.getByTestId('public-row')).toHaveCount(3)
    // into the sub folder and back
    await guest.locator('[data-testid="public-row"][data-path="/sub"] button').first().click()
    await expect(guest).toHaveURL(/\/s\/[^/]+\/sub$/)
    await expect(guest.getByTestId('public-row')).toHaveCount(1)
    await expect(guest.getByTestId('public-crumb-current')).toHaveText('sub')
    await guest.getByTestId('public-crumb').first().click()
    await expect(guest.getByTestId('public-row')).toHaveCount(3)
    // axe on the unlocked page
    const axe = await new AxeBuilder({ page: guest }).analyze()
    expect(axe.violations.filter((v) => v.impact === 'critical' || v.impact === 'serious')).toEqual(
      [],
    )
    // download one file (1/2), zip everything (2/2), the third is refused
    const dl = guest.waitForEvent('download')
    await guest
      .locator(
        '[data-testid="public-row"][data-path="/ringkasan.txt"] [data-testid="public-row-download"]',
      )
      .click()
    const file = await dl
    expect(file.suggestedFilename()).toBe('ringkasan.txt')
    const zipDl = guest.waitForEvent('download')
    await guest.getByTestId('public-zip').click()
    expect((await zipDl).suggestedFilename()).toMatch(/\.zip$/)
    const token = url.slice(url.lastIndexOf('/') + 1)
    const third = await guest.request.get(`/api/v1/public/shares/${token}/download?path=/kedua.txt`)
    expect(third.status()).toBe(410)
    expect(((await third.json()) as { error: { code: string } }).error.code).toBe(
      'SHARE_LIMIT_REACHED',
    )
    await guest.reload()
    await expect(guest.getByText('Batas unduhan link ini sudah tercapai')).toBeVisible()
    // the viewer still previews (raw is not a download)
    await guest
      .locator('[data-testid="public-row"][data-path="/ringkasan.txt"] button')
      .first()
      .click()
    await expect(guest.getByRole('dialog')).toContainText('isi ringkasan untuk tamu')
    await guest.keyboard.press('Escape')
    // the cookie is bound to the link: another share stays locked
    const other = await createShare(page.request, { path: `${DIR}/sub`, password: 'lain' })
    const locked = await guest.request.get(`/api/v1/public/shares/${other.token}/list`)
    expect(locked.status()).toBe(401)
  } finally {
    await ctx.close()
  }
})

test('preview-only file link, disable, rename and delete lifecycle', async ({ page, browser }) => {
  const share = await createShare(page.request, {
    path: `${DIR}/ringkasan.txt`,
    allowDownload: false,
    hideOwner: true,
  })
  const { ctx, page: guest } = await guestContext(browser)
  try {
    await guest.goto(share.url)
    await expect(guest.getByTestId('public-file')).toBeVisible()
    await expect(guest.getByTestId('public-download')).toHaveCount(0)
    await expect(guest.getByTestId('public-owner')).toHaveCount(0)
    await guest.getByTestId('public-preview').click()
    await expect(guest.getByRole('dialog')).toContainText('isi ringkasan untuk tamu')
    await guest.keyboard.press('Escape')
    // disabled by the owner → gone for the guest
    const dis = await apiPost(page.request, `/shares/${share.id}/disable`, {})
    expect(dis.status()).toBe(200)
    await guest.reload()
    await expect(guest.getByText('Link ini tidak tersedia')).toBeVisible()
    await expect(guest.getByText('Pemilik menonaktifkan link ini.')).toBeVisible()
    const en = await apiPost(page.request, `/shares/${share.id}/enable`, {})
    expect(en.status()).toBe(200)
    // rename the parent folder: the link keeps working
    const ren = await apiPost(page.request, '/files/rename', { path: DIR, newName: 'berbagi-2' })
    expect(ren.status(), await ren.text()).toBe(200)
    await guest.reload()
    await expect(guest.getByTestId('public-file')).toBeVisible()
    const back = await apiPost(page.request, '/files/rename', {
      path: '/berbagi-2',
      newName: 'berbagi',
    })
    expect(back.status()).toBe(200)
    // delete to the Recycle Bin: disabled with its reason; restore offers the share
    const del = await apiPost(page.request, '/files/delete', { paths: [`${DIR}/ringkasan.txt`] })
    expect(del.status()).toBe(200)
    const trashId = ((await del.json()) as { results: { trashId: string }[] }).results[0]!.trashId
    await guest.reload()
    await expect(
      guest.getByText('Berkas telah dipindahkan ke Recycle Bin oleh pemiliknya.'),
    ).toBeVisible()
    const restore = await apiPost(page.request, '/trash/restore', { ids: [trashId] })
    expect(restore.status()).toBe(200)
    expect(((await restore.json()) as { reactivateShares: string[] }).reactivateShares).toEqual([
      share.id,
    ])
    await apiPost(page.request, `/shares/${share.id}/enable`, {})
    await guest.reload()
    await expect(guest.getByTestId('public-file')).toBeVisible()
  } finally {
    await ctx.close()
  }
})

test('file request: a guest upload lands in the owner folder without overwriting', async ({
  page,
  browser,
}) => {
  const share = await createShare(page.request, {
    path: '/drop',
    allowUpload: true,
    note: 'Taruh laporan di sini',
  })
  const { ctx, page: guest } = await guestContext(browser)
  try {
    await guest.goto(share.url)
    await expect(guest.getByTestId('public-note')).toHaveText('Taruh laporan di sini')
    const f = join(work, 'laporan.txt')
    writeFileSync(f, 'laporan dari tamu')
    await guest.getByTestId('public-upload-input-file').setInputFiles(f)
    await expect(
      guest.locator('[data-testid="public-uploads"] li[data-status="done"]'),
    ).toHaveCount(1, {
      timeout: 30_000,
    })
    await expect(
      guest.locator('[data-testid="public-row"][data-path="/laporan.txt"]'),
    ).toBeVisible()
    // the same name again is renamed, never overwritten
    await guest.getByTestId('public-upload-input-file').setInputFiles(f)
    await expect(
      guest.locator('[data-testid="public-uploads"] li[data-status="done"]'),
    ).toHaveCount(2, {
      timeout: 30_000,
    })
    await expect(
      guest.locator('[data-testid="public-row"][data-path="/laporan (1).txt"]'),
    ).toBeVisible()
    // the owner sees both files and an upload notification
    const list = await page.request.get('/api/v1/files/list?path=/drop')
    const names = ((await list.json()) as { items: { name: string }[] }).items.map((i) => i.name)
    expect(names).toEqual(['laporan (1).txt', 'laporan.txt'])
    await expect(page.getByTestId('notifications-unread')).toHaveText(/[12]/)
  } finally {
    await ctx.close()
    // keep later specs clean (tasks.spec counts the badge)
    const token = await csrfToken(page.request)
    await page.request.post('/api/v1/notifications/read', {
      data: { all: true },
      headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
    })
  }
})
