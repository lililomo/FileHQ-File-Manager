import { defineConfig, devices } from '@playwright/test'

/** E2E runs against the real binary (bin/filemanager, built by `make build`) started by e2e/backend.mjs. */
export const E2E_PORT = 18090
const baseURL = `http://127.0.0.1:${E2E_PORT}`

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  use: {
    baseURL,
    locale: 'id-ID',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  // auth.spec.ts exercises the forced first-password change, so it runs before every
  // other spec (a11y.spec.ts sorts before it alphabetically since Step 21)
  projects: [
    { name: 'auth', testMatch: /auth\.spec\.ts$/, use: { ...devices['Desktop Chrome'] } },
    {
      name: 'chromium',
      testIgnore: /auth\.spec\.ts$/,
      dependencies: ['auth'],
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'node e2e/backend.mjs',
    url: `${baseURL}/healthz`,
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: 'pipe',
    stderr: 'pipe',
  },
})
