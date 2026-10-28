import AxeBuilder from '@axe-core/playwright'
import {
  expect,
  request,
  test,
  type APIRequestContext,
  type Page,
  type Request,
} from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { ADMIN_INITIAL_PASSWORD, ADMIN_NEW_PASSWORD, ADMIN_USERNAME } from './fixtures'
import { makePdf } from './pdf-fixtures'
import { startBackend, type BackendHandle } from './second-backend'

// Step 23e: separate user-content origin + sandboxed HTML preview. A second
// backend runs with CONTENT_BASE_URL=http://localhost:<port> while its app
// origin is http://127.0.0.1:<port>: same loopback, two browser origins, one
// binary routing by Host. The shared E2E server (18090) is left alone.
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const PORT = 18091
const APP = `http://127.0.0.1:${PORT}`
const CONTENT = `http://localhost:${PORT}`
const DIR = '/origin-uji'
const FFMPEG = '/usr/bin/ffmpeg'
const HTML_PAYLOAD =
  `<!doctype html><html><head><meta charset="utf-8"><title>uji</title></head><body>` +
  `<script>parent.postMessage('x','*');fetch('${APP}/api/v1/auth/me')</script>` +
  `<img src="x" onerror="parent.postMessage('y','*');fetch('${APP}/api/v1/auth/me')">` +
  `<p>teks pratinjau sandbox</p></body></html>`
// 1×1 PNG
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64',
)

let backend: BackendHandle
let admin: APIRequestContext
let videoName = ''

async function csrf(ctx: APIRequestContext): Promise<string> {
  const res = await ctx.get('/api/v1/auth/csrf')
  return ((await res.json()) as { csrfToken: string }).csrfToken
}

async function post(ctx: APIRequestContext, path: string, data: unknown) {
  return ctx.post(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': await csrf(ctx), Origin: APP },
  })
}

async function login(page: Page) {
  await page.goto(`${APP}/login`)
  await page.getByLabel('Username').fill(ADMIN_USERNAME)
  await page.getByLabel('Password', { exact: true }).fill(ADMIN_NEW_PASSWORD)
  await page.getByRole('button', { name: 'Masuk' }).click()
  await expect(page).toHaveURL(/\/files/)
}

const overlay = (page: Page) => page.getByTestId('viewer-overlay')

async function openFile(page: Page, name: string) {
  await page.goto(`${APP}/files${DIR}`)
  const row = page.getByRole('row', { name: new RegExp(name.replace(/\./g, '\\.')) })
  await expect(row).toBeVisible()
  await row.dblclick()
  await expect(overlay(page)).toBeVisible()
}

/** Requests the page sends to the content origin while `run` executes. */
async function contentRequests(page: Page, run: () => Promise<void>): Promise<Request[]> {
  const seen: Request[] = []
  const listener = (r: Request) => {
    if (r.url().startsWith(CONTENT + '/')) seen.push(r)
  }
  page.on('request', listener)
  try {
    await run()
  } finally {
    page.off('request', listener)
  }
  return seen
}

test.beforeAll(async () => {
  backend = await startBackend({ port: PORT, env: { CONTENT_BASE_URL: CONTENT } })
  // first login + forced password change through the API
  admin = await request.newContext({ baseURL: APP })
  let res = await admin.post('/api/v1/auth/login', {
    data: { username: ADMIN_USERNAME, password: ADMIN_INITIAL_PASSWORD },
    headers: { Origin: APP },
  })
  expect(res.status(), await res.text()).toBe(200)
  res = await post(admin, '/auth/change-password', {
    currentPassword: ADMIN_INITIAL_PASSWORD,
    newPassword: ADMIN_NEW_PASSWORD,
  })
  expect(res.status(), await res.text()).toBe(204)
  res = await post(admin, '/files/mkdir', { path: '/', name: DIR.slice(1) })
  expect(res.status(), await res.text()).toBe(201)
  res = await post(admin, '/files/mkdir', { path: DIR, name: 'bagikan' })
  expect(res.status(), await res.text()).toBe(201)
  const root = join(backend.storage, 'admin', DIR.slice(1))
  mkdirSync(join(root, 'bagikan'), { recursive: true })
  writeFileSync(join(root, 'gambar.png'), PNG)
  writeFileSync(join(root, 'bagikan', 'gambar.png'), PNG)
  writeFileSync(join(root, 'dokumen.pdf'), makePdf({ pages: 2 }))
  writeFileSync(join(root, 'halaman.html'), HTML_PAYLOAD)
  if (existsSync(FFMPEG)) {
    execFileSync(FFMPEG, [
      '-nostdin',
      '-v',
      'error',
      '-y',
      '-f',
      'lavfi',
      '-i',
      'testsrc=size=160x90:rate=25',
      '-t',
      '2',
      '-c:v',
      'libx264',
      '-preset',
      'ultrafast',
      '-pix_fmt',
      'yuv420p',
      '-an',
      join(root, 'klip.mp4'),
    ])
  }
})

test.afterAll(async () => {
  await admin?.dispose()
  await backend?.stop()
})

test('config advertises the content origin; app-origin inline raw redirects there', async () => {
  const me = await admin.get('/api/v1/auth/me')
  expect(((await me.json()) as { config: { contentBaseUrl: string } }).config.contentBaseUrl).toBe(
    CONTENT,
  )
  const sign = await post(admin, '/files/sign', { path: `${DIR}/gambar.png`, purpose: 'inline' })
  const { url } = (await sign.json()) as { url: string }
  expect(url.startsWith(`${CONTENT}/api/v1/files/raw?`)).toBe(true)
  const onApp = url.replace(CONTENT, APP)
  const res = await admin.get(onApp, { maxRedirects: 0 })
  expect(res.status()).toBe(302)
  expect(res.headers()['location']).toBe(url)
  // the same signature serves on the content host — with CORS for the app origin, no cookies
  const served = await admin.get(url, { headers: { Origin: APP } })
  expect(served.status()).toBe(200)
  expect(served.headers()['content-type']).toBe('image/png')
  expect(served.headers()['access-control-allow-origin']).toBe(APP)
  expect(served.headers()['cross-origin-resource-policy']).toBe('cross-origin')
  expect(served.headers()['set-cookie']).toBeUndefined()
  // preflight (Range → pdf.js / hls.js / fetchSigned)
  const pre = await admin.fetch(url, {
    method: 'OPTIONS',
    headers: {
      Origin: APP,
      'Access-Control-Request-Method': 'GET',
      'Access-Control-Request-Headers': 'range',
    },
  })
  expect(pre.status()).toBe(204)
  expect(pre.headers()['access-control-allow-methods']).toBe('GET, HEAD')
  expect(pre.headers()['access-control-allow-headers']).toBe('Range')
})

test('the content host ignores the session: / and /api/v1/auth/me are 404 even with the cookie', async () => {
  const cookies = await admin.storageState()
  const session = cookies.cookies.find((c) => c.name === 'fm_session')
  expect(session).toBeDefined()
  const hdr = { Cookie: `fm_session=${session!.value}` }
  for (const p of [
    '/',
    '/api/v1/auth/me',
    '/s/x',
    '/healthz',
    '/api/v1/files/list?path=%2F',
    '/login',
  ]) {
    const res = await admin.get(`${CONTENT}${p}`, { headers: hdr, maxRedirects: 0 })
    expect(res.status(), p).toBe(404)
    expect(res.headers()['content-type'], p).toContain('application/json')
    expect(res.headers()['set-cookie'], p).toBeUndefined()
  }
  // the same session works on the app origin
  const me = await admin.get(`${APP}/api/v1/auth/me`, { headers: hdr })
  expect(me.status()).toBe(200)
})

test('image, PDF and video viewers load from the content origin without a cookie', async ({
  page,
}) => {
  await login(page)
  const image = await contentRequests(page, async () => {
    await openFile(page, 'gambar.png')
    await expect(
      overlay(page)
        .locator('img[src^="' + CONTENT + '/api/v1/files/raw?"]')
        .first(),
    ).toBeVisible()
    await page.keyboard.press('Escape')
  })
  expect(image.length).toBeGreaterThan(0)
  const pdf = await contentRequests(page, async () => {
    await openFile(page, 'dokumen.pdf')
    await expect(
      page.locator('[data-testid="pdf-container"] .page[data-page-number="1"] canvas'),
    ).toBeVisible({ timeout: 30_000 })
    await page.keyboard.press('Escape')
  })
  expect(pdf.some((r) => r.url().includes('/api/v1/files/raw?'))).toBe(true)
  test.skip(!existsSync(FFMPEG), 'ffmpeg not installed: no video fixture')
  const video = await contentRequests(page, async () => {
    await openFile(page, 'klip.mp4')
    const v = overlay(page).getByTestId('video-element')
    await expect
      .poll(() => v.evaluate((el: HTMLVideoElement) => el.readyState), { timeout: 30_000 })
      .toBeGreaterThanOrEqual(1)
    await page.keyboard.press('Escape')
  })
  expect(video.some((r) => r.url().includes('/api/v1/files/raw?'))).toBe(true)
  for (const r of [...image, ...pdf, ...video]) {
    const h = await r.allHeaders()
    expect(h['cookie'], r.url()).toBeUndefined()
  }
  // nothing from the content origin was requested on the app origin
  expect([...image, ...pdf, ...video].every((r) => new URL(r.url()).origin === CONTENT)).toBe(true)
})

test('HTML preview renders in a sandbox: no script, no cookie, no message, no reach into the app origin', async ({
  page,
}) => {
  await login(page)
  await page.goto(`${APP}/files${DIR}`)
  await page.evaluate(() => {
    const w = window as unknown as { __messages: number }
    w.__messages = 0
    window.addEventListener('message', () => w.__messages++)
  })
  let dialogs = 0
  page.on('dialog', (d) => {
    dialogs++
    void d.dismiss()
  })
  let frameAuthMe = 0
  const frameRequests: Request[] = []
  page.on('request', (r) => {
    if (r.frame() === page.mainFrame()) return
    frameRequests.push(r)
    if (r.url().includes('/api/v1/auth/me')) frameAuthMe++
  })
  const row = page.getByRole('row', { name: /halaman\.html/ })
  await row.dblclick()
  await expect(overlay(page)).toBeVisible()
  await expect(page.getByTestId('html-tab-preview')).toHaveAttribute('aria-selected', 'true')
  const frameEl = page.getByTestId('html-preview-frame')
  await expect(frameEl).toBeVisible()
  expect(await frameEl.getAttribute('sandbox')).toBe('')
  expect(await frameEl.getAttribute('src')).toMatch(new RegExp(`^${CONTENT}/api/v1/files/raw\\?`))
  await expect(page.getByTestId('html-sandbox-note')).toHaveText(
    'Pratinjau berjalan di sandbox: skrip dan jaringan dimatikan.',
  )
  // scripts are off inside the frame, so nothing can be evaluated there;
  // the proof is request-level: the frame's own navigation carried no
  // cookie, and the rendered text is visible through the frame locator
  const frameNav = frameRequests.find((r) => r.url().startsWith(CONTENT + '/api/v1/files/raw?'))
  expect(frameNav).toBeDefined()
  expect((await frameNav!.allHeaders())['cookie']).toBeUndefined()
  expect((await frameNav!.response())?.status()).toBe(200)
  const frame = page.frameLocator('[data-testid="html-preview-frame"]')
  await expect(frame.getByText('teks pratinjau sandbox')).toBeVisible()
  const sandboxed = page.frames().find((f) => f.url().startsWith(CONTENT + '/api/v1/files/raw?'))
  expect(sandboxed).toBeDefined()
  // the document arrived inline as HTML with the preview CSP
  const doc = await admin.get(sandboxed!.url())
  expect(doc.headers()['content-type']).toBe('text/html; charset=utf-8')
  expect(doc.headers()['content-security-policy']).toBe(
    `sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'; frame-ancestors ${APP}`,
  )
  expect(doc.headers()['x-frame-options']).toBeUndefined()
  await page.waitForTimeout(1000)
  expect(await page.evaluate(() => (window as unknown as { __messages: number }).__messages)).toBe(
    0,
  )
  expect(frameAuthMe).toBe(0)
  expect(dialogs).toBe(0)
  // the frame's only completed request is its own document: `<img src=x>`
  // is blocked by the preview CSP before it leaves the browser (Chromium
  // still reports it as a failed request, reason "csp") and fetch() never
  // runs (no script). Nothing else, nothing to the app origin.
  for (const r of frameRequests) {
    if (r.url() === frameNav!.url()) continue
    expect(r.url(), 'blocked sub-resource').toBe(`${CONTENT}/api/v1/files/x`)
    expect(await r.response(), r.url()).toBeNull()
    expect(r.failure()?.errorText, r.url()).toMatch(/csp/i)
  }
  // "Tampilkan source" shows the markup itself
  await page.getByTestId('html-tab-source').click()
  await expect(page.getByTestId('code-viewer')).toBeVisible()
  await expect(page.getByTestId('code-viewer')).toContainText("parent.postMessage('x','*')")
  await expect(page.getByTestId('html-preview-frame')).toHaveCount(0)
  await page.getByTestId('html-tab-preview').click()
  await expect(page.getByTestId('html-preview-frame')).toBeVisible()
  // axe, light and dark
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme })
    // the sandboxed frame has no scripting at all: axe cannot be injected
    // there (its document is user content anyway), so the frame element is
    // excluded and the overlay around it is audited
    const results = await new AxeBuilder({ page })
      .include('[data-testid="viewer-overlay"]')
      .exclude('[data-testid="html-preview-frame"]')
      .analyze()
    const bad = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
    expect(
      bad.map((v) => `${scheme} ${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`),
    ).toEqual([])
  }
  await page.keyboard.press('Escape')
})

test('public share: the guest unlocks on the app origin and the image loads from the content origin', async ({
  browser,
}) => {
  const res = await post(admin, '/shares', { path: `${DIR}/bagikan`, password: 'rahasia-tamu' })
  expect(res.status(), await res.text()).toBe(201)
  const { url } = (await res.json()) as { url: string }
  const ctx = await browser.newContext()
  const guest = await ctx.newPage()
  try {
    await guest.goto(url)
    await guest.getByTestId('public-password').fill('rahasia-tamu')
    await guest.getByTestId('public-unlock').click()
    await expect(guest.getByTestId('public-browser')).toBeVisible()
    const reqs = await contentRequests(guest, async () => {
      await guest
        .locator('[data-testid="public-row"][data-path="/gambar.png"] button')
        .first()
        .click()
      await expect(guest.getByTestId('viewer-overlay')).toBeVisible()
      await expect(
        guest.locator('img[src^="' + CONTENT + '/api/v1/public/shares/"]').first(),
      ).toBeVisible()
    })
    const raw = reqs.filter((r) => /\/api\/v1\/public\/shares\/[^/]+\/raw\?/.test(r.url()))
    expect(raw.length).toBeGreaterThan(0)
    for (const r of raw) {
      const u = new URL(r.url())
      expect(u.searchParams.get('sig')).toBeTruthy()
      expect(u.searchParams.get('scope')).toMatch(/^s:/)
      expect((await r.allHeaders())['cookie']).toBeUndefined()
      const resp = await r.response()
      expect(resp?.status()).toBe(200)
    }
    // the cookie route on the app origin is not served there any more
    const token = url.slice(url.lastIndexOf('/') + 1)
    const direct = await guest.request.get(
      `${APP}/api/v1/public/shares/${token}/raw?path=%2Fgambar.png`,
      {
        maxRedirects: 0,
      },
    )
    expect(direct.status()).toBe(302)
    expect(direct.headers()['location']).toContain(`${CONTENT}/api/v1/public/shares/`)
    // and without a signature the content host refuses even with the cookie
    const unsigned = await guest.request.get(
      `${CONTENT}/api/v1/public/shares/${token}/raw?path=%2Fgambar.png`,
    )
    expect(unsigned.status()).toBe(403)
  } finally {
    await ctx.close()
  }
})

test('HLS: playlist and segments come from the content origin with CORS and play', async ({
  page,
}) => {
  test.skip(!existsSync(FFMPEG), 'ffmpeg not installed')
  await login(page)
  // a codec headless Chromium cannot decode (as hls.spec.ts)
  const hevc = await page.evaluate(
    () => document.createElement('video').canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"') !== '',
  )
  const root = join(backend.storage, 'admin', DIR.slice(1))
  videoName = hevc ? 'konversi.avi' : 'konversi.mkv'
  execFileSync(FFMPEG, [
    '-nostdin',
    '-v',
    'error',
    '-y',
    '-f',
    'lavfi',
    '-i',
    'testsrc=size=160x90:rate=25',
    '-f',
    'lavfi',
    '-i',
    'sine=frequency=500',
    '-t',
    '2',
    ...(hevc
      ? ['-c:v', 'mpeg4', '-q:v', '5']
      : ['-c:v', 'libx265', '-preset', 'ultrafast', '-x265-params', 'log-level=error']),
    '-pix_fmt',
    'yuv420p',
    '-c:a',
    'aac',
    '-shortest',
    join(root, videoName),
  ])
  // (through the admin API context: page.request is bound to the shared 18090 server)
  const enable = await admin.put('/api/v1/admin/settings', {
    data: { 'transcode.enabled': true, 'transcode.max_resolution': '720p' },
    headers: { 'X-CSRF-Token': await csrf(admin), Origin: APP },
  })
  expect(enable.status(), await enable.text()).toBe(200)
  const hls = await contentRequests(page, async () => {
    await openFile(page, videoName)
    const card = overlay(page).getByTestId('video-unsupported')
    await expect(card).toBeVisible()
    await card.getByTestId('video-transcode').click()
    const player = overlay(page).getByTestId('video-player')
    await expect(player).toBeVisible({ timeout: 60_000 })
    const video = overlay(page).getByTestId('video-element')
    await expect
      .poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 60_000 })
      .toBeGreaterThanOrEqual(2)
    await overlay(page).getByTestId('video-toggle').click()
    await expect
      .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 30_000 })
      .toBeGreaterThan(0)
    await expect(player).toHaveAttribute('data-hls', 'hlsjs')
  })
  const playlist = hls.filter((r) =>
    /\/api\/v1\/files\/hls\/[0-9a-f-]+\/playlist\.m3u8\?/.test(r.url()),
  )
  const segments = hls.filter((r) =>
    /\/api\/v1\/files\/hls\/[0-9a-f-]+\/seg_\d{5}\.ts\?/.test(r.url()),
  )
  expect(playlist.length).toBeGreaterThan(0)
  expect(segments.length).toBeGreaterThan(0)
  for (const r of [...playlist, ...segments]) {
    expect((await r.allHeaders())['cookie'], r.url()).toBeUndefined()
    const resp = await r.response()
    // 202 = playlist polled before two segments existed (hls.spec semantics)
    expect([200, 202, 206]).toContain(resp?.status())
    expect(resp?.headers()['access-control-allow-origin']).toBe(APP)
  }
  await page.keyboard.press('Escape')
})
