/** A second backend instance for specs that need a different configuration
 *  than the shared E2E server of e2e/backend.mjs (Step 23e: CONTENT_BASE_URL).
 *  Same binary, same credentials, own scratch STORAGE_ROOT/DATA_DIR; started
 *  in a spec's beforeAll and stopped in afterAll. */
import { spawn, type ChildProcess } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { ADMIN_INITIAL_PASSWORD, ADMIN_USERNAME } from './fixtures'

export interface BackendHandle {
  port: number
  /** APP_BASE_URL (http://127.0.0.1:<port>) */
  app: string
  /** STORAGE_ROOT of the instance (user folders live below it) */
  storage: string
  env: Record<string, string>
  stop: () => Promise<void>
}

export async function startBackend(opts: {
  port: number
  env?: Record<string, string>
}): Promise<BackendHandle> {
  const bin = resolve(process.cwd(), '..', 'bin', 'filemanager')
  if (!existsSync(bin)) throw new Error(`${bin} not found — run \`make build\` first`)
  const dir = mkdtempSync(join(tmpdir(), 'fm-content-'))
  const storage = join(dir, 'storage')
  mkdirSync(join(storage, '.system'), { recursive: true })
  const app = `http://127.0.0.1:${opts.port}`
  const env: Record<string, string> = {
    PATH: process.env.PATH ?? '',
    HOME: process.env.HOME ?? '/tmp',
    APP_ENV: 'development',
    ALLOW_ROOT: 'true',
    APP_BASE_URL: app,
    LISTEN_ADDR: `127.0.0.1:${opts.port}`,
    TRUSTED_PROXIES: '127.0.0.1',
    STORAGE_ROOT: storage,
    DATA_DIR: join(storage, '.system'),
    APP_SECRET: randomBytes(32).toString('hex'),
    ADMIN_USERNAME,
    ADMIN_INITIAL_PASSWORD,
    ADMIN_CAN_BROWSE_USERS: 'false',
    LOG_LEVEL: 'warn',
    ...(opts.env ?? {}),
  }
  const child: ChildProcess = spawn(bin, ['serve'], {
    env,
    stdio: ['ignore', 'inherit', 'inherit'],
  })
  let exited = false
  child.on('exit', () => {
    exited = true
  })
  const deadline = Date.now() + 60_000
  for (;;) {
    if (exited) throw new Error('second backend exited before it was ready')
    try {
      const res = await fetch(`${app}/healthz`)
      if (res.ok) break
    } catch {
      /* not up yet */
    }
    if (Date.now() > deadline) {
      child.kill('SIGKILL')
      throw new Error('second backend did not become ready in 60 s')
    }
    await new Promise((r) => setTimeout(r, 200))
  }
  return {
    port: opts.port,
    app,
    storage,
    env,
    stop: () =>
      new Promise<void>((done) => {
        if (exited) {
          rmSync(dir, { recursive: true, force: true })
          done()
          return
        }
        child.once('exit', () => {
          rmSync(dir, { recursive: true, force: true })
          done()
        })
        child.kill('SIGTERM')
        setTimeout(() => child.kill('SIGKILL'), 10_000).unref()
      }),
  }
}
