import { expect, test, type Page, type Response } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { apiMkdir, apiResetPrefs, e2eStorageRoot, loginAsAdmin } from './fixtures'

// Step 17b: music player + mini-player, video player (Video.js 10), text /
// CSV / JSON / Markdown viewers and Quick Look. Media fixtures are generated
// with ffmpeg at run time (nothing big is committed).
test.describe.configure({ mode: 'serial', timeout: 120_000 })

const FFMPEG = '/usr/bin/ffmpeg'
const DIR = '/pemutar'

function ff(...args: string[]) {
  execFileSync(FFMPEG, ['-nostdin', '-v', 'error', '-y', ...args])
}

let seeded = false
function seed(root: string) {
  if (seeded) return
  seeded = true
  const musik = join(root, 'musik')
  const video = join(root, 'video')
  const data = join(root, 'data')
  for (const d of [musik, video, data]) mkdirSync(d, { recursive: true })
  // audio: 3 tracks, the first with tags and an embedded cover
  const cover = join(root, 'cover.png')
  ff('-f', 'lavfi', '-i', 'color=c=orange:s=64x64:d=1', '-frames:v', '1', cover)
  ff(
    '-f',
    'lavfi',
    '-i',
    'sine=frequency=440:duration=40',
    '-i',
    cover,
    '-map',
    '0:a',
    '-map',
    '1:v',
    '-c:a',
    'libmp3lame',
    '-b:a',
    '64k',
    '-c:v',
    'mjpeg',
    '-id3v2_version',
    '3',
    '-metadata',
    'title=Lagu Satu',
    '-metadata',
    'artist=Penyanyi Uji',
    '-metadata',
    'album=Album Uji',
    '-disposition:v',
    'attached_pic',
    join(musik, '01-satu.mp3'),
  )
  ff(
    '-f',
    'lavfi',
    '-i',
    'sine=frequency=660:duration=30',
    '-c:a',
    'libmp3lame',
    '-b:a',
    '64k',
    join(musik, '02-dua.mp3'),
  )
  ff(
    '-f',
    'lavfi',
    '-i',
    'sine=frequency=880:duration=30',
    '-c:a',
    'libmp3lame',
    '-b:a',
    '64k',
    join(musik, '03-tiga.mp3'),
  )
  // video: H.264/AAC with a subtitle sidecar, a bigger one for the expiry
  // test (not fully buffered), and an HEVC MKV the browser cannot decode
  ff(
    '-f',
    'lavfi',
    '-i',
    'testsrc=size=320x240:rate=25',
    '-f',
    'lavfi',
    '-i',
    'sine=frequency=300',
    '-t',
    '30',
    '-c:v',
    'libx264',
    '-pix_fmt',
    'yuv420p',
    '-c:a',
    'aac',
    '-shortest',
    '-movflags',
    '+faststart',
    join(video, 'a-klip.mp4'),
  )
  writeFileSync(
    join(video, 'a-klip.srt'),
    '1\r\n00:00:00,000 --> 00:00:59,000\r\n<font color="red">Halo subtitle uji</font><script>x</script>\r\n',
  )
  ff(
    '-f',
    'lavfi',
    '-i',
    'testsrc2=size=1280x720:rate=30',
    '-t',
    '60',
    '-c:v',
    'libx264',
    '-preset',
    'ultrafast',
    '-b:v',
    '6M',
    '-pix_fmt',
    'yuv420p',
    '-movflags',
    '+faststart',
    join(video, 'b-besar.mp4'),
  )
  ff(
    '-f',
    'lavfi',
    '-i',
    'testsrc=size=160x120:rate=10',
    '-t',
    '2',
    '-c:v',
    'libx265',
    '-pix_fmt',
    'yuv420p',
    '-x265-params',
    'log-level=error',
    join(video, 'c-film.mkv'),
  )
  // text fixtures
  const rows = ['id;nama;nilai']
  for (let i = 1; i <= 10_000; i++) rows.push(`${i};"Nama ${i}; ke-${i}";${i * 3}`)
  writeFileSync(join(data, 'besar.csv'), rows.join('\n') + '\n')
  writeFileSync(
    join(data, 'data.json'),
    JSON.stringify({
      proyek: 'uji',
      versi: 2,
      tim: [{ nama: 'Budi' }, { nama: 'Ani' }],
      config: { dalam: { kunciRahasia: 42 } },
    }),
  )
  writeFileSync(join(data, 'catatan.txt'), 'baris pertama catatan\nbaris kedua\n')
  writeFileSync(
    join(data, 'README.md'),
    '# Judul\n\n<script>window.__xss=1</script>\n\n<img src=x onerror="window.__xss=2">\n\n[tautan](javascript:alert(1))\n\nTeks **tebal**.\n',
  )
}

test.beforeEach(async ({ page }) => {
  test.skip(!existsSync(FFMPEG), 'ffmpeg not installed')
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    await apiMkdir(page.request, '/', 'pemutar')
    seed(join(e2eStorageRoot(), 'admin', 'pemutar'))
  }
})

const row = (page: Page, name: RegExp) => page.getByRole('row', { name })
const overlay = (page: Page) => page.getByTestId('viewer-overlay')

async function openDir(page: Page, dir: string, first: RegExp) {
  await page.goto(`/files${dir}`)
  await expect(row(page, first)).toBeVisible()
}

/** Seconds shown by a "m:ss" element. */
async function shownSeconds(page: Page, testId: string): Promise<number> {
  const txt = (await page.getByTestId(testId).first().textContent()) ?? ''
  const [m, s] = txt.trim().split(' ')[0]!.split(':').map(Number)
  return (m ?? 0) * 60 + (s ?? 0)
}

test('music: plays across folder changes and dialogs, Media Session, playlist next', async ({
  page,
}) => {
  await openDir(page, `${DIR}/musik`, /01-satu\.mp3/)
  await row(page, /01-satu\.mp3/).dblclick()
  const dialog = page.getByTestId('player-dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog.getByTestId('audio-title')).toHaveText('Lagu Satu')
  await expect(dialog.getByTestId('audio-artist')).toContainText('Penyanyi Uji')
  await expect(dialog.getByTestId('audio-cover')).toBeVisible()
  await expect(dialog.getByTestId('playlist-item')).toHaveCount(3)
  await expect
    .poll(() => shownSeconds(page, 'audio-time'), { timeout: 15_000 })
    .toBeGreaterThanOrEqual(1)
  // Media Session (SPEC §8.14): title / artist / album / cover
  const ms = await page.evaluate(() => {
    const m = navigator.mediaSession.metadata
    return m ? { title: m.title, artist: m.artist, album: m.album, art: m.artwork.length } : null
  })
  expect(ms).toEqual({ title: 'Lagu Satu', artist: 'Penyanyi Uji', album: 'Album Uji', art: 1 })

  // close the full player → mini-player keeps playing while the folder changes
  await page.keyboard.press('Escape')
  const mini = page.getByTestId('mini-player')
  await expect(mini).toBeVisible()
  await expect(mini.getByTestId('mini-player-title')).toHaveText('Lagu Satu')
  await page.getByTestId(`crumb-${DIR}`).click() // up to /media (in-app navigation)
  const videoDir = page.locator(`[role="row"][data-path="${DIR}/video"]`)
  await expect(videoDir).toBeVisible()
  await videoDir.dblclick()
  await expect(row(page, /a-klip\.mp4/)).toBeVisible()
  const t1 = await shownSeconds(page, 'audio-time')
  await expect.poll(() => shownSeconds(page, 'audio-time'), { timeout: 10_000 }).toBeGreaterThan(t1)
  await expect(mini.getByTestId('audio-toggle')).toHaveAttribute('data-playing', 'true')
  // a dialog (command palette) does not stop it either
  await page.keyboard.press('Control+k')
  await expect(page.getByRole('dialog')).toBeVisible()
  const t2 = await shownSeconds(page, 'audio-time')
  await expect.poll(() => shownSeconds(page, 'audio-time'), { timeout: 10_000 }).toBeGreaterThan(t2)
  await page.keyboard.press('Escape')
  // another route (Recent) keeps the mini-player
  await page
    .getByRole('link', { name: /Terbaru|Recent/ })
    .first()
    .click()
  await expect(page).toHaveURL(/\/recent/)
  await expect(mini).toBeVisible()

  // playlist next → the second track, Media Session follows
  await mini.getByTestId('audio-next').click()
  await expect(mini.getByTestId('mini-player-title')).toHaveText('02-dua.mp3')
  await expect
    .poll(() => page.evaluate(() => navigator.mediaSession.metadata?.title))
    .toBe('02-dua.mp3')
  await expect
    .poll(() => shownSeconds(page, 'audio-time'), { timeout: 15_000 })
    .toBeGreaterThanOrEqual(1)
  // expand → playlist marks the current track; stop clears the player
  await mini.getByTestId('mini-player-expand').click()
  await expect(page.getByTestId('player-dialog').locator('[data-current="true"]')).toContainText(
    '02-dua.mp3',
  )
  await page.keyboard.press('Escape')
  await mini.getByTestId('mini-player-close').click()
  await expect(mini).toHaveCount(0)
})

test('"Tambah ke playlist" from the context menu appends the selection', async ({ page }) => {
  await openDir(page, `${DIR}/musik`, /01-satu\.mp3/)
  await row(page, /02-dua\.mp3/).click()
  await row(page, /03-tiga\.mp3/).click({ modifiers: ['Control'] })
  await row(page, /03-tiga\.mp3/).click({ button: 'right', modifiers: ['Control'] })
  await page.getByTestId('cm-addToPlaylist').click()
  await expect(page.getByText('2 lagu ditambahkan ke playlist')).toBeVisible()
  const mini = page.getByTestId('mini-player')
  await expect(mini).toBeVisible()
  await expect(mini.getByTestId('mini-player-title')).toHaveText('02-dua.mp3')
  await mini.getByTestId('mini-player-close').click()
})

test('video: Video.js chunk on demand, 206 seek, SRT subtitle as VTT, PiP, resume prompt', async ({
  page,
}) => {
  const urls: string[] = []
  page.on('request', (r) => urls.push(r.url()))
  const ranged: Response[] = []
  page.on('response', (r) => {
    if (r.url().includes('/api/v1/files/raw') && r.url().includes('a-klip.mp4')) ranged.push(r)
  })
  await openDir(page, `${DIR}/video`, /a-klip\.mp4/)
  expect(urls.filter((u) => /VideoPlayer-/.test(u))).toEqual([])
  await row(page, /a-klip\.mp4/).dblclick()
  await expect(overlay(page).getByTestId('video-player')).toBeVisible()
  expect(urls.some((u) => /VideoPlayer-/.test(u))).toBeTruthy()
  const video = page.getByTestId('video-element')
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 15_000 })
    .toBeGreaterThanOrEqual(1)
  expect(ranged.some((r) => r.status() === 206)).toBeTruthy()

  // subtitle: the .srt sidecar arrives as sanitised WebVTT and shows
  await expect
    .poll(
      () =>
        video.evaluate((v: HTMLVideoElement) => {
          const tr = v.textTracks[0]
          return tr
            ? `${tr.mode}|${tr.cues?.length ?? 0}|${(tr.cues?.[0] as VTTCue | undefined)?.text ?? ''}`
            : ''
        }),
      { timeout: 15_000 },
    )
    .toBe('showing|1|Halo subtitle ujix')
  const vtt = await page.evaluate(async () => {
    const src = document.querySelector('track')?.getAttribute('src') ?? ''
    const r = await fetch(src)
    return { type: r.headers.get('content-type'), body: await r.text() }
  })
  expect(vtt.type).toBe('text/vtt; charset=utf-8')
  expect(vtt.body).toContain('00:00:00.000 --> 00:00:59.000')
  expect(vtt.body).not.toContain('<font')
  expect(vtt.body).not.toContain('<script')

  // play, then seek with L (+10 s): a ranged request, playback continues
  await page.getByTestId('video-toggle').click()
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime))
    .toBeGreaterThan(0.3)
  await page.keyboard.press('l')
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 10_000 })
    .toBeGreaterThan(10)
  expect(ranged.filter((r) => r.status() === 206).length).toBeGreaterThan(0)
  // PiP is available (button enabled)
  await expect(page.getByTestId('video-pip')).toBeEnabled()
  // pause at ~10 s and leave: the position is stored
  await page.keyboard.press('Space')
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.paused)).toBe(true)
  const at = await video.evaluate((v: HTMLVideoElement) => v.currentTime)
  await page.keyboard.press('Escape')
  await expect(overlay(page)).toBeHidden()
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(
              `/api/v1/me/media-position?path=${encodeURIComponent(`${DIR}/video/a-klip.mp4`)}`,
            )
          ).json()
        ).seconds,
    )
    .toBeGreaterThan(9)

  // open again → "Lanjutkan dari 0:1x?" → resume at the stored position
  await row(page, /a-klip\.mp4/).dblclick()
  const prompt = page.getByTestId('video-resume')
  await expect(prompt).toBeVisible()
  await expect(prompt).toContainText(/Lanjutkan dari 0:1\d\?/)
  await prompt.getByTestId('video-resume-yes').click()
  await expect
    .poll(() => page.getByTestId('video-element').evaluate((v: HTMLVideoElement) => v.currentTime))
    .toBeGreaterThan(at - 1)
  await page.keyboard.press('Escape')
})

test('video: an expired signed URL is re-signed and playback resumes at the same spot', async ({
  page,
}) => {
  await openDir(page, `${DIR}/video`, /b-besar\.mp4/)
  await page.evaluate(() => ((window as unknown as { __noReload: number }).__noReload = 1))
  await row(page, /b-besar\.mp4/).dblclick()
  const video = page.getByTestId('video-element')
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 15_000 })
    .toBeGreaterThanOrEqual(1)
  await page.getByTestId('video-toggle').click()
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime))
    .toBeGreaterThan(0.5)
  const oldSrc = await video.evaluate((v: HTMLVideoElement) => v.currentSrc)
  const oldSig = new URL(oldSrc).searchParams.get('sig')!
  // from now on the old URL is "expired" (as after MEDIA_URL_TTL)
  let refused = 0
  await page.route('**/api/v1/files/raw?**', async (route) => {
    if (route.request().url().includes(`sig=${oldSig}`)) {
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
  // jump to 90 %: outside the buffered range → a new request with the old URL
  await page.keyboard.press('9')
  await expect.poll(() => refused, { timeout: 15_000 }).toBeGreaterThan(0)
  await expect
    .poll(
      () => video.evaluate((v: HTMLVideoElement) => new URL(v.currentSrc).searchParams.get('sig')),
      { timeout: 15_000 },
    )
    .not.toBe(oldSig)
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 15_000 })
    .toBeGreaterThan(50)
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => !v.paused && !v.error)).toBe(true)
  expect(await page.evaluate(() => (window as unknown as { __noReload?: number }).__noReload)).toBe(
    1,
  )
  await page.unroute('**/api/v1/files/raw?**')
  await page.keyboard.press('Escape')
})

test('video: HEVC in MKV → clear message and download, no black player', async ({ page }) => {
  await openDir(page, `${DIR}/video`, /c-film\.mkv/)
  await row(page, /c-film\.mkv/).dblclick()
  const card = overlay(page).getByTestId('video-unsupported')
  await expect(card).toBeVisible()
  await expect(card).toContainText('Codec HEVC (H.265) tidak didukung browser ini')
  await expect(card.getByTestId('video-download')).toBeVisible()
  await expect(overlay(page).locator('video')).toHaveCount(0)
  await page.keyboard.press('Escape')
})

test('CSV 10k rows scrolls virtualised; JSON tree; Markdown sanitised; text Edit → editor', async ({
  page,
}) => {
  await openDir(page, `${DIR}/data`, /besar\.csv/)
  await row(page, /besar\.csv/).dblclick()
  const csv = page.getByTestId('csv-viewer')
  await expect(csv.getByTestId('csv-count')).toContainText('10.000 baris', { timeout: 15_000 })
  await expect(csv).toContainText('Pemisah: ;')
  await expect(csv.getByRole('columnheader', { name: /nama/ })).toBeVisible()
  await expect(csv.locator('[data-row="0"]')).toContainText('Nama 1; ke-1')
  const scroll = csv.getByTestId('csv-scroll')
  await scroll.evaluate((el) => (el.scrollTop = el.scrollHeight))
  await expect(csv.locator('[data-row="9999"]')).toContainText('Nama 10000; ke-10000')
  expect(await csv.locator('[data-row]').count()).toBeLessThan(200)
  await page.keyboard.press('Escape')

  await row(page, /data\.json/).dblclick()
  const tree = page.getByTestId('json-tree')
  await expect(tree).toBeVisible()
  await expect(tree.locator('[data-ptr="/tim/0"]')).toBeVisible()
  await tree.getByTestId('json-search').fill('rahasia')
  await expect(tree.getByTestId('json-matches')).toHaveText('1 dari 1')
  await expect(tree.locator('[data-ptr="/config/dalam/kunciRahasia"]')).toContainText('42')
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await expect(overlay(page)).toBeHidden()

  await row(page, /README\.md/).dblclick()
  const md = page.getByTestId('markdown-rendered')
  await expect(md.getByRole('heading', { name: 'Judul' })).toBeVisible()
  await expect(md.locator('script')).toHaveCount(0)
  await expect(md.locator('[onerror]')).toHaveCount(0)
  await expect(md.locator('a[href^="javascript:"]')).toHaveCount(0)
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined()
  await page.getByTestId('markdown-tab-source').click()
  await expect(page.getByTestId('code-viewer')).toContainText('<script>window.__xss=1</script>')
  await page.keyboard.press('Escape')

  // text: read-only viewer first, "Edit" opens the editor
  await row(page, /catatan\.txt/).dblclick()
  await expect(page.getByTestId('code-viewer')).toContainText('baris pertama catatan')
  await page.getByTestId('viewer-edit').click()
  await expect(page.getByTestId('editor-modal')).toBeVisible()
})

test('Quick Look: text head opens fast, arrows move, Space closes', async ({ page }) => {
  await openDir(page, `${DIR}/data`, /catatan\.txt/)
  await row(page, /catatan\.txt/).click()
  await page.keyboard.press('Space')
  const ql = page.getByTestId('quick-look')
  await expect(ql.getByTestId('text-head')).toContainText('baris pertama catatan')
  await page.keyboard.press('Space')
  await expect(ql).toHaveCount(0)
  // second open (cached): measured in the page from the Space keydown to the visible text
  await page.evaluate(() => {
    const w = window as unknown as { __qlMs?: Promise<number> }
    w.__qlMs = new Promise<number>((resolve) => {
      let t0 = 0
      window.addEventListener('keydown', (e) => (e.key === ' ' ? (t0 = performance.now()) : 0), {
        capture: true,
        once: true,
      })
      const obs = new MutationObserver(() => {
        if (t0 && document.querySelector('[data-testid="quick-look"] [data-testid="text-head"]')) {
          obs.disconnect()
          resolve(performance.now() - t0)
        }
      })
      obs.observe(document.body, { childList: true, subtree: true })
    })
  })
  await page.keyboard.press('Space')
  const ms = await page.evaluate(() => (window as unknown as { __qlMs: Promise<number> }).__qlMs)
  console.log(`[perf] Quick Look txt (cached) open: ${ms.toFixed(0)} ms`)
  expect(ms).toBeLessThan(300)
  await expect(ql).toBeVisible()
  // → moves to the next item of the listing (data.json) and the grid follows
  await page.keyboard.press('ArrowRight')
  await expect(ql.getByTestId('quick-look-name')).toHaveText('data.json')
  // (the grid is aria-hidden behind the modal: CSS locator)
  await expect(page.locator(`[role="row"][data-path="${DIR}/data/data.json"]`)).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await page.keyboard.press('Escape')
  await expect(ql).toHaveCount(0)
})
