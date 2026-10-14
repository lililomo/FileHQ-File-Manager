// Starts the real backend binary on a scratch DATA_DIR/STORAGE_ROOT for Playwright.
// Credentials here must match e2e/fixtures.ts.
import { spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const PORT = 18090
const bin = resolve(process.cwd(), '..', 'bin', 'filemanager')
if (!existsSync(bin)) {
  console.error(`e2e: ${bin} not found — run \`make build\` first`)
  process.exit(2)
}
// Only one E2E backend runs at a time (fixed port, reuseExistingServer: false), so any
// leftover fm-e2e-* directory belongs to a run that was killed before its cleanup ran
// (SIGKILL from Playwright's webServer timeout). Sweep them so /tmp does not fill up.
for (const name of readdirSync(tmpdir())) {
  if (name.startsWith('fm-e2e-') && name !== 'fm-e2e-storage.txt')
    rmSync(join(tmpdir(), name), { recursive: true, force: true })
}
const dir = mkdtempSync(join(tmpdir(), 'fm-e2e-'))
const storage = join(dir, 'storage')
mkdirSync(join(storage, '.system'), { recursive: true })
// Specs that need direct filesystem fixtures (10k files) read the storage root from here.
writeFileSync(join(tmpdir(), 'fm-e2e-storage.txt'), storage)

const env = {
  PATH: process.env.PATH,
  HOME: process.env.HOME ?? '/tmp',
  APP_ENV: 'development',
  ALLOW_ROOT: 'true',
  APP_BASE_URL: `http://127.0.0.1:${PORT}`,
  LISTEN_ADDR: `127.0.0.1:${PORT}`,
  TRUSTED_PROXIES: '127.0.0.1',
  STORAGE_ROOT: storage,
  DATA_DIR: join(storage, '.system'),
  APP_SECRET: randomBytes(32).toString('hex'),
  ADMIN_USERNAME: 'admin',
  ADMIN_INITIAL_PASSWORD: 'E2e-Initial-Passw0rd!',
  ADMIN_CAN_BROWSE_USERS: 'false',
  LOG_LEVEL: 'warn',
  // Step 11 tasks.spec: slow each copied file by 2 ms so a 5 000-file copy can be cancelled mid-way
  DEV_TASK_ITEM_DELAY_MS: '2',
  // Step 12a upload.spec: 1 MB chunks so pause/resume/abort can happen mid-file
  UPLOAD_CHUNK_SIZE: '1MB',
  // Step 13 remote-download.spec: the local test server lives on loopback, which
  // production always blocks (development-only exemption)
  DEV_REMOTE_DOWNLOAD_ALLOW_LOOPBACK: 'true',
}

const child = spawn(bin, ['serve'], { env, stdio: 'inherit' })
const cleanup = () => {
  try {
    child.kill('SIGTERM')
  } catch {
    /* already gone */
  }
  rmSync(dir, { recursive: true, force: true })
}
for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP'])
  process.on(sig, () => {
    cleanup()
    process.exit(0)
  })
process.on('exit', cleanup)
child.on('exit', (code) => {
  rmSync(dir, { recursive: true, force: true })
  process.exit(code ?? 0)
})
