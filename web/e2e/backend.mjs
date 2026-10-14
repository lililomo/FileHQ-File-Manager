// Starts the real backend binary on a scratch DATA_DIR/STORAGE_ROOT for Playwright.
// Credentials here must match e2e/fixtures.ts.
import { spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const PORT = 18090
const bin = resolve(process.cwd(), '..', 'bin', 'filemanager')
if (!existsSync(bin)) {
  console.error(`e2e: ${bin} not found — run \`make build\` first`)
  process.exit(2)
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
