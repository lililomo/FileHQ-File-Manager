import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { createHmac } from 'node:crypto'
import { csrfToken, loginAsAdmin } from './fixtures'

/** Step 23a (SPEC §8.1 step 5, §8.17, §8.18): nina enables TOTP from her
 *  profile (reading the manual secret from the dialog, computing the code
 *  here like an authenticator app would), the login then asks for a code
 *  (wrong → inline error, right → signed in), a recovery code works once and
 *  the limiter answers 429 in the UI, 2FA is turned off with password + code,
 *  and after an API re-enrolment the admin resets it from /admin/users so
 *  nina signs in without a code again. The user is purged afterwards. */

test.describe.configure({ mode: 'serial', timeout: 150_000 })

const ORIGIN = 'http://127.0.0.1:18090'
const USERNAME = 'nina23a'
const TEMP_PASSWORD = 'Nina-sementara-2fa-2026!'
const PASSWORD = 'Nina-password-2fa-2026!'
const PERIOD = 30

let secret: Buffer = Buffer.alloc(0)
let lastStep = -1
let recoveryCodes: string[] = []

// --- authenticator in the test ---------------------------------------------

function base32Decode(input: string): Buffer {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = 0
  let value = 0
  const out: number[] = []
  for (const ch of input.replace(/[\s=-]/g, '').toUpperCase()) {
    const idx = alphabet.indexOf(ch)
    if (idx < 0) throw new Error(`invalid base32 character ${ch}`)
    value = (value << 5) | idx
    bits += 5
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff)
      bits -= 8
    }
  }
  return Buffer.from(out)
}

function hotp(key: Buffer, counter: number): string {
  const msg = Buffer.alloc(8)
  msg.writeBigUInt64BE(BigInt(counter))
  const h = createHmac('sha1', key).update(msg).digest()
  const off = h[h.length - 1]! & 0x0f
  const code =
    (((h[off]! & 0x7f) << 24) | (h[off + 1]! << 16) | (h[off + 2]! << 8) | h[off + 3]!) % 1_000_000
  return code.toString().padStart(6, '0')
}

const currentStep = () => Math.floor(Date.now() / 1000 / PERIOD)

/** A code the server has not seen yet: the current step, or the next one
 *  (still inside the ±1 window) when the current step was already used;
 *  waits for the clock only when both were consumed. */
async function freshCode(): Promise<string> {
  let step = Math.max(currentStep(), lastStep + 1)
  if (step > currentStep() + 1) {
    await new Promise((r) => setTimeout(r, (step - 1) * PERIOD * 1000 - Date.now() + 500))
    step = Math.max(currentStep(), lastStep + 1)
  }
  lastStep = step
  return hotp(secret, step)
}

// --- helpers -----------------------------------------------------------------

async function apiPost(
  ctx: APIRequestContext,
  path: string,
  data: unknown,
  method: 'post' | 'delete' = 'post',
) {
  const token = await csrfToken(ctx)
  return ctx[method](`/api/v1${path}`, { data, headers: { 'X-CSRF-Token': token, Origin: ORIGIN } })
}

async function fillCredentials(page: Page) {
  await page.goto('/login')
  await page.getByLabel('Username').fill(USERNAME)
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD)
  await page.getByRole('button', { name: 'Masuk' }).click()
}

async function submitCode(page: Page, code: string) {
  await page.getByTestId('login-totp').fill(code)
  await page.getByTestId('login-totp-submit').click()
}

async function cleanup(page: Page) {
  const list = await page.request.get(`/api/v1/admin/users?q=${USERNAME}`)
  if (!list.ok()) return
  const body = (await list.json()) as { items: { id: string; username: string }[] }
  const u = body.items.find((i) => i.username === USERNAME)
  if (!u) return
  const del = await apiPost(
    page.request,
    `/admin/users/${u.id}`,
    { confirmUsername: USERNAME, mode: 'purge' },
    'delete',
  )
  if (del.status() === 202) {
    const task = ((await del.json()) as { task: { id: string } }).task
    for (let i = 0; i < 50; i++) {
      const t = await page.request.get(`/api/v1/tasks/${task.id}`)
      const state = ((await t.json()) as { task: { state: string } }).task.state
      if (state === 'done' || state === 'failed') break
      await page.waitForTimeout(200)
    }
    await apiPost(page.request, `/tasks/${task.id}`, undefined, 'delete')
  }
  await apiPost(page.request, '/notifications/read', { all: true })
}

test.beforeAll(async ({ browser }) => {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await loginAsAdmin(page)
  await cleanup(page)
  const created = await apiPost(page.request, '/admin/users', {
    username: USERNAME,
    displayName: 'Nina 2FA',
    role: 'user',
    temporaryPassword: TEMP_PASSWORD,
  })
  expect(created.status(), await created.text()).toBe(201)
  await ctx.close()
  // nina settles her password through the API so the UI tests start on /files
  const nina = await browser.newContext()
  const login = await nina.request.post('/api/v1/auth/login', {
    data: { username: USERNAME, password: TEMP_PASSWORD },
    headers: { Origin: ORIGIN },
  })
  expect(login.status(), await login.text()).toBe(200)
  const changed = await apiPost(nina.request, '/auth/change-password', {
    currentPassword: TEMP_PASSWORD,
    newPassword: PASSWORD,
  })
  expect(changed.status(), await changed.text()).toBe(204)
  await nina.close()
})

test.afterAll(async ({ browser }) => {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await loginAsAdmin(page)
  await cleanup(page)
  await ctx.close()
})

test('nina enables 2FA from her profile and keeps the recovery codes', async ({ page }) => {
  await fillCredentials(page)
  await expect(page).toHaveURL(/\/files/)
  await page.goto('/settings')
  const card = page.getByTestId('twofa-card')
  await expect(card.getByTestId('twofa-status')).toHaveText('Nonaktif')
  await card.getByTestId('twofa-enable').click()
  const dialog = page.getByTestId('twofa-enable-dialog')
  await expect(dialog).toBeVisible()
  await expect(
    dialog.getByRole('img', { name: 'Kode QR untuk aplikasi authenticator' }),
  ).toBeVisible()
  const b32 = await dialog.getByTestId('totp-secret').getAttribute('data-secret')
  expect(b32).toMatch(/^[A-Z2-7]{32}$/)
  secret = base32Decode(b32!)
  lastStep = -1
  // a wrong code is refused inline
  await dialog.getByTestId('twofa-code').fill('000000')
  await dialog.getByTestId('twofa-verify').click()
  await expect(dialog.getByRole('alert')).toContainText('Kode salah')
  await dialog.getByTestId('twofa-code').fill(await freshCode())
  await dialog.getByTestId('twofa-verify').click()
  const codes = dialog.getByTestId('recovery-code')
  await expect(codes).toHaveCount(10)
  recoveryCodes = await codes.allInnerTexts()
  for (const c of recoveryCodes) expect(c).toMatch(/^[a-z2-9]{5}-[a-z2-9]{5}$/)
  // the dialog cannot be closed before the codes are acknowledged
  const done = dialog.getByTestId('twofa-done')
  await expect(done).toBeDisabled()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeVisible()
  await dialog.getByTestId('recovery-ack').click()
  await expect(done).toBeEnabled()
  await done.click()
  await expect(dialog).toBeHidden()
  await expect(card.getByTestId('twofa-status')).toHaveText('Aktif')
  await expect(card.getByTestId('twofa-disable')).toBeVisible()
  const me = (await (await page.request.get('/api/v1/auth/me')).json()) as {
    user: { totpEnabled: boolean; totpEnabledAt?: string }
  }
  expect(me.user.totpEnabled).toBe(true)
  expect(me.user.totpEnabledAt).toBeTruthy()
})

test('login asks for the code: a wrong code fails inline, the right one signs in', async ({
  page,
}) => {
  await fillCredentials(page)
  const input = page.getByTestId('login-totp')
  await expect(input).toBeVisible()
  await expect(input).toBeFocused()
  await expect(page).toHaveURL(/\/login/)
  await submitCode(page, '000000')
  await expect(page.locator('#login-totp-error')).toContainText('Kode salah')
  await expect(page).toHaveURL(/\/login/)
  await submitCode(page, await freshCode())
  await expect(page).toHaveURL(/\/files/)
  await expect(page.getByTestId('user-menu')).toContainText('Nina')
})

test('a recovery code works once; too many attempts answer 429 in the UI', async ({
  page,
  browser,
}) => {
  await fillCredentials(page)
  await page.getByTestId('login-totp-toggle').click()
  await expect(page.getByLabel('Kode pemulihan')).toBeVisible()
  await submitCode(page, recoveryCodes[0]!.toUpperCase())
  await expect(page).toHaveURL(/\/files/)
  // The per-user limiter (5 verifications per 15 s in e2e) is shared by every
  // code check: clear its window, then spend four verifications through the
  // session API (wrong code on /me/totp/disable) so the login form shows the
  // 429 after two more attempts without hammering the per-IP login limiter.
  await page.waitForTimeout(16_000)
  for (let i = 0; i < 4; i++) {
    const r = await apiPost(page.request, '/me/totp/disable', {
      password: PASSWORD,
      code: '000000',
    })
    expect(r.status(), await r.text()).toBe(400)
  }
  // second use of the same code from another browser: refused (5th check)
  const other = await browser.newContext()
  const p2 = await other.newPage()
  await fillCredentials(p2)
  await p2.getByTestId('login-totp-toggle').click()
  await submitCode(p2, recoveryCodes[0]!)
  await expect(p2.locator('#login-totp-error')).toContainText('Kode pemulihan salah')
  // 6th check: the limiter answers 429 and the form shows the countdown
  await submitCode(p2, 'aaaaa-aaaaa')
  await expect(
    p2.getByRole('alert').filter({ hasText: 'Terlalu banyak percobaan kode' }),
  ).toBeVisible()
  await expect(p2.getByTestId('login-totp-submit')).toBeDisabled()
  // after the window the next code is accepted again
  await p2.waitForTimeout(16_000)
  await submitCode(p2, recoveryCodes[1]!)
  await expect(p2).toHaveURL(/\/files/)
  await other.close()
})

test('nina turns 2FA off with her password and a code', async ({ page }) => {
  await fillCredentials(page)
  await submitCode(page, await freshCode())
  await expect(page).toHaveURL(/\/files/)
  await page.goto('/settings')
  const card = page.getByTestId('twofa-card')
  await expect(card.getByTestId('twofa-status')).toHaveText('Aktif')
  await card.getByTestId('twofa-disable').click()
  const dialog = page.getByTestId('twofa-disable-dialog')
  await expect(dialog).toBeVisible()
  await dialog.getByTestId('twofa-disable-password').fill('salah-' + PASSWORD)
  await dialog.getByTestId('twofa-disable-code').fill(recoveryCodes[2]!)
  await dialog.getByTestId('twofa-disable-submit').click()
  await expect(dialog.getByRole('alert')).toContainText('Password salah')
  await dialog.getByTestId('twofa-disable-password').fill(PASSWORD)
  await dialog.getByTestId('twofa-disable-code').fill(await freshCode())
  await dialog.getByTestId('twofa-disable-submit').click()
  await expect(dialog).toBeHidden()
  await expect(card.getByTestId('twofa-status')).toHaveText('Nonaktif')
  const me = (await (await page.request.get('/api/v1/auth/me')).json()) as {
    user: { totpEnabled: boolean }
  }
  expect(me.user.totpEnabled).toBe(false)
})

test('admin resets 2FA from the user list; nina signs in without a code', async ({
  page,
  browser,
}) => {
  // nina re-enrols through the API (fresh secret)
  const nina = await browser.newContext()
  const login = await nina.request.post('/api/v1/auth/login', {
    data: { username: USERNAME, password: PASSWORD },
    headers: { Origin: ORIGIN },
  })
  expect(login.status(), await login.text()).toBe(200)
  const setup = await apiPost(nina.request, '/me/totp/setup', {})
  expect(setup.status(), await setup.text()).toBe(200)
  secret = base32Decode(((await setup.json()) as { secret: string }).secret)
  lastStep = -1
  const enable = await apiPost(nina.request, '/me/totp/enable', { code: await freshCode() })
  expect(enable.status(), await enable.text()).toBe(200)
  // admin: badge + Reset 2FA
  await loginAsAdmin(page)
  await page.goto('/admin/users')
  const row = page.locator(`[data-testid="user-row"][data-username="${USERNAME}"]`)
  await expect(row.getByTestId('user-twofa-badge')).toBeVisible()
  await row.getByTestId('user-menu').click()
  await page.getByTestId('user-action-reset-totp').click()
  await page.getByTestId('user-confirm-resetTotp-confirm').click()
  await expect(row.getByTestId('user-twofa-badge')).toHaveCount(0)
  await row.getByTestId('user-menu').click()
  await expect(page.getByTestId('user-action-reset-totp')).toHaveCount(0)
  await page.keyboard.press('Escape')
  // nina's session is gone and the next login needs no code
  const me = await nina.request.get('/api/v1/auth/me')
  expect(me.status()).toBe(401)
  await nina.close()
  const fresh = await browser.newContext()
  const p2 = await fresh.newPage()
  await fillCredentials(p2)
  await expect(p2).toHaveURL(/\/files/)
  await expect(p2.getByTestId('login-totp')).toHaveCount(0)
  await fresh.close()
})
