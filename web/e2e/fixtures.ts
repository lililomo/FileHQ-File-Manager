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
  await page.getByRole('button', { name: 'Masuk' }).click()
  const alert = page.getByRole('alert')
  const outcome = await Promise.race([
    page.waitForURL(/\/(files|change-password)/).then(() => 'nav' as const),
    alert.waitFor({ state: 'visible' }).then(() => 'alert' as const),
  ])
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
