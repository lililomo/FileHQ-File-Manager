import { expect, test } from '@playwright/test'
import { ADMIN_INITIAL_PASSWORD, ADMIN_NEW_PASSWORD, ADMIN_USERNAME } from './fixtures'

test.describe.configure({ mode: 'serial' })

test('theme is applied before first paint without inline scripts', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('theme', 'dark'))
  const response = await page.goto('/login')
  expect(response?.headers()['content-security-policy']).toContain("default-src 'self'")
  expect(response?.headers()['content-security-policy']).not.toContain('unsafe-inline')
  await expect(page.locator('script:not([src])')).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.dataset.theme)).toBe('dark')
  await expect(page.getByRole('heading', { name: 'Masuk' })).toBeVisible()
})

test('language toggle switches the login page to English', async ({ page }) => {
  await page.goto('/login')
  await page.getByTestId('language-toggle').click()
  await page.getByRole('menuitemradio', { name: 'English' }).click()
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.lang)).toBe('en')
  await page.getByTestId('language-toggle').click()
  await page.getByRole('menuitemradio', { name: 'Bahasa Indonesia' }).click()
  await expect(page.getByRole('heading', { name: 'Masuk' })).toBeVisible()
})

test('wrong password shows a generic message', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('Username').fill(ADMIN_USERNAME)
  await page.getByLabel('Password', { exact: true }).fill('definitely-wrong')
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page.getByRole('alert')).toHaveText('Username atau password salah.')
})

test('admin: login → forced password change → shell → logout', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('Username').fill(ADMIN_USERNAME)
  await page.getByLabel('Password', { exact: true }).fill(ADMIN_INITIAL_PASSWORD)
  await page.getByRole('button', { name: 'Masuk' }).click()

  await expect(page).toHaveURL(/\/change-password$/)
  await expect(page.getByRole('heading', { name: 'Ganti password' })).toBeVisible()
  // The shell is blocked while the password change is pending.
  await page.goto('/files')
  await expect(page).toHaveURL(/\/change-password$/)

  await page.getByLabel('Password saat ini').fill(ADMIN_INITIAL_PASSWORD)
  await page.getByLabel('Password baru', { exact: true }).fill(ADMIN_NEW_PASSWORD)
  await page.getByLabel('Ulangi password baru').fill(ADMIN_NEW_PASSWORD)
  await page.getByRole('button', { name: 'Simpan password' }).click()

  await expect(page).toHaveURL(/\/files\/?$/)
  await expect(page.getByTestId('user-menu')).toContainText(ADMIN_USERNAME)
  await expect(page.getByRole('navigation', { name: 'Navigasi utama' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Admin' })).toBeVisible()

  await page.getByTestId('user-menu').click()
  await page.getByTestId('logout').click()
  await expect(page).toHaveURL(/\/login/)
  await page.goto('/files')
  await expect(page).toHaveURL(/\/login\?next=/)
})

test('new password works and the old one is rejected', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('Username').fill(ADMIN_USERNAME)
  await page.getByLabel('Password', { exact: true }).fill(ADMIN_INITIAL_PASSWORD)
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page.getByRole('alert')).toHaveText('Username atau password salah.')
  await page.getByLabel('Password', { exact: true }).fill(ADMIN_NEW_PASSWORD)
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page).toHaveURL(/\/files\/?$/)
})
