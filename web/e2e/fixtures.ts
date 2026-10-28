/** Must match e2e/backend.mjs. */
export const ADMIN_USERNAME = 'admin'
export const ADMIN_INITIAL_PASSWORD = 'E2e-Initial-Passw0rd!'
export const ADMIN_NEW_PASSWORD = 'CorrectHorse-Battery-Staple-42'

import { readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { APIRequestContext, Page } from '@playwright/test'
import { expect } from '@playwright/test'

/** Storage root of the running e2e backend (written by e2e/backend.mjs). */
export function e2eStorageRoot(): string {
  return readFileSync(join(tmpdir(), 'fm-e2e-storage.txt'), 'utf8').trim()
}

/** Logs in as admin whatever the current password is (initial or already changed by auth.spec). */
export async function loginAsAdmin(page: Page): Promise<void> {
  await page.goto('/login')
  await page.getByLabel('Username').fill(ADMIN_USERNAME)
  await page.getByLabel('Password', { exact: true }).fill(ADMIN_NEW_PASSWORD)
  const submit = page.getByRole('button', { name: 'Masuk' })
  await submit.click()
  // only the wrong-password alert switches to the initial password (another
  // alert, e.g. a toast, must not win the race — R-F/23a/23b flake)
  const alert = page.getByRole('alert').filter({ hasText: 'Username atau password salah' })
  // the full suite logs in more than RATE_LIMIT_LOGIN_IP (20/min) allows at
  // times: a 429 shows this alert, so wait out Retry-After and submit again
  // (Step 23c: the real cause of the responsive.spec flake)
  const limited = page.getByRole('alert').filter({ hasText: 'Terlalu banyak permintaan' })
  let outcome: 'nav' | 'alert' | 'limited' = 'limited'
  for (let attempt = 0; attempt < 3 && outcome === 'limited'; attempt++) {
    outcome = await Promise.race([
      // 'commit': the URL is asserted again below
      page
        .waitForURL(/\/(files|change-password)/, { waitUntil: 'commit' })
        .then(() => 'nav' as const),
      alert.waitFor({ state: 'visible' }).then(() => 'alert' as const),
      limited.waitFor({ state: 'visible' }).then(() => 'limited' as const),
    ])
    if (outcome === 'limited') {
      const seconds = Number(/(\d+) detik/.exec((await limited.textContent()) ?? '')?.[1] ?? 30)
      await page.waitForTimeout(Math.min(seconds + 1, 65) * 1000)
      await expect(submit).toBeEnabled({ timeout: 10_000 })
      await submit.click()
      await limited.waitFor({ state: 'hidden', timeout: 5_000 }).catch(() => undefined)
    }
  }
  if (outcome === 'alert') {
    await page.getByLabel('Password', { exact: true }).fill(ADMIN_INITIAL_PASSWORD)
    await page.getByRole('button', { name: 'Masuk' }).click()
    await page.waitForURL(/\/change-password/)
  }
  if (page.url().includes('/change-password')) {
    await page.getByLabel('Password saat ini').fill(ADMIN_INITIAL_PASSWORD)
    await page.getByLabel('Password baru', { exact: true }).fill(ADMIN_NEW_PASSWORD)
    await page.getByLabel('Ulangi password baru').fill(ADMIN_NEW_PASSWORD)
    await page.getByRole('button', { name: 'Simpan password' }).click()
  }
  await expect(page).toHaveURL(/\/files/)
}

/** CSRF token for API calls made through the page's request context (shares cookies). */
export async function csrfToken(request: APIRequestContext): Promise<string> {
  const res = await request.get('/api/v1/auth/csrf')
  expect(res.ok()).toBeTruthy()
  return ((await res.json()) as { csrfToken: string }).csrfToken
}

export async function apiMkdir(
  request: APIRequestContext,
  path: string,
  name: string,
): Promise<void> {
  const token = await csrfToken(request)
  const res = await request.post('/api/v1/files/mkdir', {
    data: { path, name },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
  expect(res.status(), await res.text()).toBe(201)
}

export async function apiCreateFile(
  request: APIRequestContext,
  path: string,
  name: string,
  content = '',
): Promise<void> {
  const token = await csrfToken(request)
  const res = await request.post('/api/v1/files/create', {
    data: { path, name, content },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
  expect(res.status(), await res.text()).toBe(201)
}

/** Reset the server-side preferences (so suites do not leak columns/sort/tabs). */
export async function apiResetPrefs(request: APIRequestContext): Promise<void> {
  const token = await csrfToken(request)
  const res = await request.put('/api/v1/me/preferences', {
    data: {
      prefs: {
        'view.columns': null,
        'view.sort': null,
        'tabs.open': null,
        'ui.preview_panel': null,
        'dualpane.enabled': null,
        'dualpane.right': null,
      },
    },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
  expect(res.status(), await res.text()).toBe(200)
}

/** Permanently deletes paths (cleanup of specs that must not leave folders in the
 *  admin root for the specs running after them). Missing paths are ignored. */
export async function apiDeletePermanent(
  request: APIRequestContext,
  paths: string[],
): Promise<void> {
  const token = await csrfToken(request)
  const res = await request.post('/api/v1/files/delete-permanent', {
    data: { paths },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
  expect([200, 202, 404], await res.text()).toContain(res.status())
}

/** Sets server-side preferences (null = back to the default). */
export async function apiSetPrefs(
  request: APIRequestContext,
  prefs: Record<string, unknown>,
): Promise<void> {
  const token = await csrfToken(request)
  const res = await request.put('/api/v1/me/preferences', {
    data: { prefs },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
  expect(res.status(), await res.text()).toBe(200)
}
