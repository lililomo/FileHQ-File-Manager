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
