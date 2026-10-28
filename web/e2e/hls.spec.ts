import { expect, test, type Page, type Request } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { apiDeletePermanent, apiMkdir, csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'

// Step 23d: on-the-fly HLS transcoding. The E2E backend has the real ffmpeg;
// the fixtures are generated here in a codec headless Chromium cannot play
// (HEVC in MKV, else MPEG-4 Part 2 in AVI). transcode.enabled is switched on
// through the admin settings API for this file and reset afterwards.
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const FFMPEG = '/usr/bin/ffmpeg'
const DIR = '/hls-uji'
const ORIGIN = 'http://127.0.0.1:18090'
const KEYS = ['transcode.enabled', 'transcode.max_resolution']

let small = 'kecil.mkv'
let big = 'besar.mkv'
const timings: Record<string, number> = {}

function ff(...args: string[]) {
  execFileSync(FFMPEG, ['-nostdin', '-v', 'error', '-y', ...args])
}

async function settings(page: Page, body: Record<string, unknown>) {
  const token = await csrfToken(page.request)
  const res = await page.request.put('/api/v1/admin/settings', {
    data: body,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
}

async function resetSettings(page: Page) {
  const token = await csrfToken(page.request)
  const res = await page.request.post('/api/v1/admin/settings/reset', {
    data: { keys: KEYS },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(res.status(), await res.text()).toBe(200)
}

async function task(page: Page, id: string) {
  const res = await page.request.get(`/api/v1/tasks/${id}`)
  return ((await res.json()) as { task: { state: string } }).task
}

async function cancel(page: Page, id: string) {
  const token = await csrfToken(page.request)
  await page.request.post(`/api/v1/tasks/${id}/cancel`, {
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
}

test.beforeAll(async ({ browser }) => {
  test.skip(!existsSync(FFMPEG), 'ffmpeg not installed')
  const page = await browser.newPage()
  await loginAsAdmin(page)
  await settings(page, { 'transcode.enabled': true, 'transcode.max_resolution': '720p' })
  await apiMkdir(page.request, '/', 'hls-uji')
  // a codec this Chromium cannot decode
  const hevc = await page.evaluate(
    () => document.createElement('video').canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"') !== '',
  )
  const root = join(e2eStorageRoot(), 'admin', 'hls-uji')
  mkdirSync(root, { recursive: true })
  const started = Date.now()
  const enc = hevc
    ? ['-c:v', 'mpeg4', '-q:v', '5']
    : ['-c:v', 'libx265', '-preset', 'ultrafast', '-x265-params', 'log-level=error']
  if (hevc) {
    small = 'kecil.avi'
    big = 'besar.avi'
  }
  // 2 s 160×90 with a tone; 40 s 1280×720 (≈ 10 s to convert: slow enough
  // to watch playback start "on the fly")
  ff(
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
    ...enc,
    '-pix_fmt',
    'yuv420p',
    '-c:a',
    'aac',
    '-shortest',
    join(root, small),
  )
  ff(
    '-f',
    'lavfi',
    '-i',
    'testsrc2=size=1280x720:rate=30',
    '-t',
    '40',
    ...enc,
    '-pix_fmt',
    'yuv420p',
    join(root, big),
  )
  timings.generateMs = Date.now() - started
  await page.close()
})

test.afterAll(async ({ browser }) => {
  if (!existsSync(FFMPEG)) return
  const page = await browser.newPage()
  await loginAsAdmin(page)
  await resetSettings(page)
  await apiDeletePermanent(page.request, [DIR])
  await page.close()
  console.log(`hls.spec timings: ${JSON.stringify(timings)}`)
})

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
})

const overlay = (page: Page) => page.getByTestId('viewer-overlay')

async function openVideo(page: Page, name: string) {
  await page.goto(`/files${DIR}`)
  const row = page.getByRole('row', { name: new RegExp(name.replace('.', '\\.')) })
  await expect(row).toBeVisible()
  await row.dblclick()
  await expect(overlay(page)).toBeVisible()
}

async function waitPlaying(page: Page) {
  const video = overlay(page).getByTestId('video-element')
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 60_000 })
    .toBeGreaterThanOrEqual(2)
  await overlay(page).getByTestId('video-toggle').click()
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 30_000 })
    .toBeGreaterThan(0)
  return video
}

test('unsupported codec + enabled → "Putar dengan konversi" plays through HLS', async ({
  page,
}) => {
  const segments: Request[] = []
  page.on('requestfinished', (r) => {
    if (/\/api\/v1\/files\/hls\/[0-9a-f-]+\/seg_\d{5}\.ts\?/.test(r.url())) segments.push(r)
  })
  await openVideo(page, small)
  const card = overlay(page).getByTestId('video-unsupported')
  await expect(card).toBeVisible()
  await expect(card).toContainText('tidak didukung browser ini')
  const button = card.getByTestId('video-transcode')
  await expect(button).toHaveText('Putar dengan konversi (lambat)')
  await expect(card.getByTestId('video-download')).toBeVisible()
  const t0 = Date.now()
  await button.click()
  const player = overlay(page).getByTestId('video-player')
  await expect(player).toBeVisible({ timeout: 60_000 })
  await waitPlaying(page)
  timings.smallClickToPlayMs = Date.now() - t0
  const mode = await player.getAttribute('data-hls')
  timings[`mode:${mode}`] = 1
  // Chromium answers "maybe" for HLS itself, but only Safari/iOS go native
  expect(mode).toBe('hlsjs')
  expect(segments.length).toBeGreaterThan(0)
  const seg = await segments[0]!.response()
  expect([200, 206]).toContain(seg?.status())
  expect(seg?.headers()['content-type']).toBe('video/mp2t')
  // the same file again reuses the finished conversion (200, no new task)
  const token = await csrfToken(page.request)
  const again = await page.request.post('/api/v1/files/hls', {
    data: { path: `${DIR}/${small}` },
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN },
  })
  expect(again.status()).toBe(200)
  expect(((await again.json()) as { ready: boolean }).ready).toBe(true)
  await page.keyboard.press('Escape')
})

test('playback starts while ffmpeg is still converting; expired signature is re-signed', async ({
  page,
}) => {
  let taskId = ''
  page.on('response', async (r) => {
    if (r.url().endsWith('/api/v1/files/hls') && r.request().method() === 'POST' && !taskId) {
      taskId = ((await r.json()) as { task: { id: string } }).task.id
    }
  })
  await openVideo(page, big)
  const t0 = Date.now()
  await overlay(page).getByTestId('video-transcode').click()
  const video = await waitPlaying(page)
  timings.bigClickToPlayMs = Date.now() - t0
  expect(taskId).not.toBe('')
  // on the fly: the conversion of the 40 s video is still running
  expect((await task(page, taskId)).state).toBe('running')
  await expect(overlay(page).getByTestId('video-player')).toHaveAttribute('data-hls', 'hlsjs')
  // from now on the current folder signature is "expired" (MEDIA_URL_TTL)
  const oldSig = await page.evaluate(
    () =>
      performance
        .getEntriesByType('resource')
        .map((e) => e.name)
        .filter((n) => n.includes('/seg_'))
        .map((n) => new URL(n).searchParams.get('sig'))
        .pop() ?? '',
  )
  expect(oldSig).not.toBe('')
  let refused = 0
  await page.route('**/api/v1/files/hls/**', async (route) => {
    if (new URL(route.request().url()).searchParams.get('sig') === oldSig) {
      refused++
      await route.fulfill({
        status: 403,
        contentType: 'application/json',
        body: JSON.stringify({
          error: { code: 'SIGNATURE_EXPIRED', message: 'signed url expired' },
        }),
      })
      return
    }
    await route.continue()
  })
  await expect.poll(() => refused, { timeout: 30_000 }).toBeGreaterThan(0)
  const before = await video.evaluate((v: HTMLVideoElement) => v.currentTime)
  await expect
    .poll(
      () =>
        page.evaluate(
          (sig) =>
            performance
              .getEntriesByType('resource')
              .some(
                (e) =>
                  e.name.includes('/seg_') &&
                  !e.name.includes(`sig=${sig}`) &&
                  e.name.includes('sig='),
              ),
          oldSig,
        ),
      { timeout: 30_000 },
    )
    .toBe(true)
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 30_000 })
    .toBeGreaterThan(before + 0.5)
  await page.unroute('**/api/v1/files/hls/**')
  await page.keyboard.press('Escape')
  await cancel(page, taskId)
  await expect
    .poll(async () => (await task(page, taskId)).state, { timeout: 15_000 })
    .toMatch(/cancelled|done/)
})

test('progress card with ETA and "Batalkan" cancels the conversion', async ({ page }) => {
  // hold the player back so the progress card stays (the playlist "is not ready")
  await page.route('**/api/v1/files/hls/*/playlist.m3u8?**', (route) =>
    route.fulfill({ status: 202, headers: { 'Retry-After': '2' }, body: '' }),
  )
  let taskId = ''
  page.on('response', async (r) => {
    if (r.url().endsWith('/api/v1/files/hls') && r.request().method() === 'POST' && !taskId) {
      taskId = ((await r.json()) as { task: { id: string } }).task.id
    }
  })
  await openVideo(page, big)
  await overlay(page).getByTestId('video-transcode').click()
  const card = overlay(page).getByTestId('video-transcode-card')
  await expect(card).toBeVisible()
  await expect(card.getByRole('progressbar')).toBeVisible()
  await expect(card.getByTestId('video-transcode-meta')).toContainText(
    /\d+ % · \d+:\d\d dari 0:40/,
    {
      timeout: 30_000,
    },
  )
  expect(taskId).not.toBe('')
  expect((await task(page, taskId)).state).toBe('running')
  await card.getByTestId('video-transcode-cancel').click()
  await expect(overlay(page).getByTestId('video-unsupported')).toBeVisible()
  await expect(overlay(page).getByTestId('video-transcode')).toBeVisible()
  await expect
    .poll(async () => (await task(page, taskId)).state, { timeout: 15_000 })
    .toBe('cancelled')
  await page.unroute('**/api/v1/files/hls/*/playlist.m3u8?**')
  await page.keyboard.press('Escape')
})

test('transcode.enabled = false → no conversion button', async ({ page }) => {
  await settings(page, { 'transcode.enabled': false })
  await page.reload()
  await openVideo(page, small)
  const card = overlay(page).getByTestId('video-unsupported')
  await expect(card).toBeVisible()
  await expect(card.getByTestId('video-download')).toBeVisible()
  await expect(card.getByTestId('video-transcode')).toHaveCount(0)
  await expect(card).toContainText('Unduh file untuk memutarnya di aplikasi lain.')
  await page.keyboard.press('Escape')
})
