import { expect, test, type APIRequestContext } from '@playwright/test'
import { apiCreateFile, apiMkdir, apiResetPrefs, csrfToken, loginAsAdmin } from './fixtures'

/**
 * Step 20 (SPEC §10.3, §10.5, §10.6 items 7, 12, 13, 14): the final CSP is
 * enforced without console violations on the main pages, security headers
 * are present on HTML and API answers, the session cookie carries the right
 * flags, the public share page never sends a Referer, and the login rate
 * limit is shown as a friendly message instead of a broken form.
 * PDF/video/Monaco under CSP are exercised by review-d.spec.ts.
 */

const DIR = '/keamanan'
const ORIGIN = 'http://127.0.0.1:18090'

const consoleErrors: string[] = []
let seeded = false
let shareId = ''
let shareToken = ''

async function apiPost(request: APIRequestContext, path: string, data: unknown) {
  const token = await csrfToken(request)
  return request.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

function cspViolations(): string[] {
  return consoleErrors.filter((m) => /Content Security Policy|Refused to|pageerror/i.test(m))
}

test.beforeEach(async ({ page }) => {
  consoleErrors.length = 0
  page.on('console', (m) => {
    if (m.type() === 'error') consoleErrors.push(m.text())
  })
  page.on('pageerror', (e) => consoleErrors.push(`pageerror: ${e.message}`))
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'keamanan')
    await apiCreateFile(
      page.request,
      DIR,
      'catatan.md',
      '# Judul\n\n<script>window.__xss=1</script> teks **tebal**\n',
    )
    await apiCreateFile(page.request, DIR, 'data.txt', 'baris 1\nbaris 2\n')
    const res = await apiPost(page.request, '/shares', { path: DIR })
    expect(res.status(), await res.text()).toBe(201)
    const json = (await res.json()) as { share: { id: string }; url: string }
    shareId = json.share.id
    shareToken = json.url.slice(json.url.lastIndexOf('/') + 1)
  }
})

test.afterAll(async ({ browser }) => {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await loginAsAdmin(page)
  if (shareId) {
    const token = await csrfToken(page.request)
    await page.request.delete(`/api/v1/shares/${shareId}`, {
      headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
    })
  }
  // permanent: other specs count Recycle Bin items
  await apiPost(page.request, '/files/delete-permanent', { paths: [DIR] })
  await ctx.close()
})

test('security headers on HTML and API answers; final CSP has no unsafe-inline/eval for scripts and CORS is off', async ({
  page,
}) => {
  const html = await page.request.get('/files')
  const h = html.headers()
  const csp = h['content-security-policy'] ?? ''
  expect(csp).toContain("default-src 'self'")
  expect(csp).toMatch(/script-src 'self' 'wasm-unsafe-eval'(;|$)/)
  expect(csp).not.toMatch(/script-src[^;]*'unsafe-inline'/)
  expect(csp).not.toContain("'unsafe-eval'")
  expect(csp).toContain("object-src 'none'")
  expect(csp).toContain("frame-ancestors 'none'")
  expect(csp).toContain("form-action 'self'")
  expect(h['x-content-type-options']).toBe('nosniff')
  expect(h['x-frame-options']).toBe('DENY')
  expect(h['referrer-policy']).toBe('strict-origin-when-cross-origin')
  expect(h['permissions-policy']).toContain('camera=()')
  expect(h['cross-origin-opener-policy']).toBe('same-origin')
  expect(h['cross-origin-resource-policy']).toBe('same-origin')
  expect(h['x-robots-tag']).toContain('noindex')
  expect(h['access-control-allow-origin']).toBeUndefined()
  expect(h['x-request-id']).toMatch(/^[0-9a-f]{32}$/)

  const api = await page.request.get('/api/v1/auth/me')
  expect(api.headers()['content-security-policy']).toBe(
    "default-src 'none'; frame-ancestors 'none'",
  )
  expect(api.headers()['cache-control']).toBe('no-store')
  expect(api.headers()['ratelimit-limit']).toMatch(/^\d+$/)
  expect(api.headers()['ratelimit-policy']).toMatch(/^\d+;w=\d+$/)

  const cookies = await page.context().cookies()
  const session = cookies.find((c) => c.name === 'fm_session')
  expect(session, 'session cookie present').toBeDefined()
  expect(session?.httpOnly).toBe(true)
  expect(session?.sameSite).toBe('Lax')
  expect(session?.path).toBe('/')
})

test('CSP enforced: browser, editor, markdown viewer, admin, settings and public page run without violations', async ({
  page,
}) => {
  await page.goto(`/files${DIR}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  await page
    .locator(`[data-testid="details-body"] [role="row"][data-path="${DIR}/catatan.md"]`)
    .dblclick()
  await expect(page.getByTestId('viewer-overlay')).toBeVisible()
  await expect(page.getByTestId('viewer-overlay')).toContainText('tebal')
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined()
  await page.keyboard.press('Escape')
  await page.goto(`/editor?path=${encodeURIComponent(`${DIR}/data.txt`)}`)
  await expect(page.getByTestId('editor-tab').first()).toContainText('data.txt', {
    timeout: 30_000,
  })
  await page.locator('.monaco-editor').first().waitFor({ state: 'visible', timeout: 30_000 })
  for (const path of [
    '/admin',
    '/admin/users',
    '/admin/settings',
    '/settings',
    '/shares',
    '/search',
  ]) {
    await page.goto(path)
    await expect(page.locator('body')).toBeVisible()
    await page.waitForTimeout(300)
  }
  await page.goto(`/s/${shareToken}`)
  await expect(page.getByTestId('public-browser')).toBeVisible()
  expect(cspViolations()).toEqual([])
})

test('public share page: Referrer-Policy no-referrer and no Referer on its API calls', async ({
  browser,
}) => {
  const ctx = await browser.newContext()
  const guest = await ctx.newPage()
  const referers: string[] = []
  guest.on('request', (r) => {
    if (r.url().includes('/api/v1/public/shares/')) referers.push(r.headers()['referer'] ?? '')
  })
  const resp = await guest.goto(`/s/${shareToken}`)
  expect(resp?.headers()['referrer-policy']).toBe('no-referrer')
  await expect(guest.getByTestId('public-browser')).toBeVisible()
  await expect(guest.getByTestId('public-list')).toContainText('data.txt')
  expect(referers.length).toBeGreaterThan(0)
  expect(referers.every((r) => r === '')).toBe(true)
  await ctx.close()
})

test('login rate limit is shown as a friendly message with the retry delay', async ({
  browser,
}) => {
  // the E2E backend trusts 127.0.0.1 as proxy, so a forged client address
  // keeps this test's limit away from the other specs
  const ctx = await browser.newContext({ extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.77' } })
  const page = await ctx.newPage()
  let limited = 0
  for (let i = 0; i < 21; i++) {
    const r = await ctx.request.post('/api/v1/auth/login', {
      data: { username: 'tidak-ada', password: `salah-${i}` }, // unknown user: IP limit without locking the admin account
      headers: { Origin: ORIGIN },
    })
    if (r.status() === 429) {
      limited++
      expect(r.headers()['retry-after']).toMatch(/^\d+$/)
    }
  }
  expect(limited, '21st attempt within a minute is rate limited').toBeGreaterThan(0)
  await page.goto('/login')
  await page.getByLabel('Username').fill('tidak-ada')
  await page.getByLabel('Password', { exact: true }).fill('salah-lagi')
  await page.getByRole('button', { name: 'Masuk' }).click()
  const alert = page.getByRole('alert')
  await expect(alert).toBeVisible()
  expect(await alert.textContent()).toMatch(/\d+/)
  // the form keeps the button disabled during the countdown (friendly, not broken)
  await expect(page.getByRole('button', { name: 'Masuk' })).toBeDisabled()
  await ctx.close()
})
