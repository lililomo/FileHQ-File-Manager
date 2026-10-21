import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { apiMkdir, apiResetPrefs, csrfToken, e2eStorageRoot, loginAsAdmin } from './fixtures'
import { makePdf } from './pdf-fixtures'

/** Review D UX pass: screenshots (light + dark, desktop + 390 px) of the
 *  search page, editor dialogs, viewers, players and Quick Look into
 *  docs/reviews/review-D/. Only runs with REVIEW_D_SHOTS=1 (it is not part
 *  of the gate; the regressions live in review-d.spec.ts). */
test.describe.configure({ mode: 'serial', timeout: 180_000 })
test.skip(!process.env.REVIEW_D_SHOTS, 'screenshots only on demand (REVIEW_D_SHOTS=1)')

const ORIGIN = 'http://127.0.0.1:18090'
const DIR = '/ux'
const FFMPEG = '/usr/bin/ffmpeg'
const VIPS = '/usr/bin/vips'
const OUT = resolve(process.cwd(), '..', 'docs', 'reviews', 'review-D')

const md = [
  '# Laporan Mingguan',
  '',
  'Ringkasan pekerjaan tim **infrastruktur** minggu ini.',
  '',
  '## Selesai',
  '',
  '- Migrasi database ke server baru',
  '- Pembaruan sertifikat TLS',
  '- [Dokumen arsitektur](arsitektur.md)',
  '',
  '## Tabel',
  '',
  '| Layanan | Status | Catatan |',
  '|---|---|---|',
  '| API | OK | latensi 42 ms |',
  '| Web | OK | — |',
  '',
  '```go',
  'func main() { fmt.Println("halo") }',
  '```',
].join('\n')

const code = [
  'package main',
  '',
  'import "fmt"',
  '',
  '// catatan: contoh file sumber untuk viewer',
  'func main() {',
  '\tfor i := 0; i < 3; i++ {',
  '\t\tfmt.Printf("baris %d\\n", i)',
  '\t}',
  '}',
  '',
].join('\n')

/** Minimal TIFF with Make/Model/Orientation (little endian, IFD0 at 8). */
function exifTIFF(): Buffer {
  const entries: { tag: number; typ: number; data: Buffer }[] = [
    { tag: 0x010f, typ: 2, data: Buffer.from('Canon\0') },
    { tag: 0x0110, typ: 2, data: Buffer.from('Canon EOS R6\0') },
    { tag: 0x0112, typ: 3, data: Buffer.from([1, 0]) },
  ]
  const head = Buffer.from([0x49, 0x49, 0x2a, 0x00, 8, 0, 0, 0])
  const ifd = Buffer.alloc(2 + entries.length * 12 + 4)
  ifd.writeUInt16LE(entries.length, 0)
  let valuesOff = 8 + ifd.length
  const values: Buffer[] = []
  entries.forEach((e, i) => {
    const o = 2 + i * 12
    ifd.writeUInt16LE(e.tag, o)
    ifd.writeUInt16LE(e.typ, o + 2)
    ifd.writeUInt32LE(e.typ === 2 ? e.data.length : 1, o + 4)
    if (e.data.length <= 4) e.data.copy(ifd, o + 8)
    else {
      ifd.writeUInt32LE(valuesOff, o + 8)
      values.push(e.data)
      valuesOff += e.data.length
    }
  })
  return Buffer.concat([head, ifd, ...values])
}
function withExif(jpeg: Buffer): Buffer {
  const payload = Buffer.concat([Buffer.from('Exif\0\0'), exifTIFF()])
  const app1 = Buffer.from([0xff, 0xe1, (payload.length + 2) >> 8, (payload.length + 2) & 0xff])
  return Buffer.concat([jpeg.subarray(0, 2), app1, payload, jpeg.subarray(2)])
}

let seeded = false
function seed() {
  if (seeded) return
  seeded = true
  const root = join(e2eStorageRoot(), 'admin', 'ux')
  for (const d of ['dokumen', 'musik', 'video', 'foto'])
    mkdirSync(join(root, d), { recursive: true })
  const doc = join(root, 'dokumen')
  writeFileSync(
    join(doc, 'catatan.txt'),
    'Catatan rapat 21 Oktober\n\n- agenda satu\n- agenda dua\n',
  )
  writeFileSync(join(doc, 'README.md'), md)
  writeFileSync(join(doc, 'main.go'), code)
  const rows = ['id,nama,kota,saldo']
  for (let i = 1; i <= 400; i++)
    rows.push(
      `${i},Pengguna ${i},${['Banyuwangi', 'Jember', 'Surabaya'][i % 3]},${(i * 12345) % 100000}`,
    )
  writeFileSync(join(doc, 'data.csv'), rows.join('\n') + '\n')
  writeFileSync(
    join(doc, 'config.json'),
    JSON.stringify(
      {
        aplikasi: 'file-manager',
        versi: '1.0.0',
        fitur: { search: true, editor: { tema: 'system', fontSize: 14 }, viewer: ['image', 'pdf'] },
        server: [
          { host: 'a.local', port: 8080 },
          { host: 'b.local', port: 8081 },
        ],
      },
      null,
      2,
    ),
  )
  writeFileSync(
    join(doc, 'laporan.pdf'),
    makePdf({
      pages: 12,
      text: (n) => `Laporan tahunan — halaman ${n}`,
      outline: [
        { title: 'Pendahuluan', page: 1 },
        { title: 'Metode', page: 3 },
        { title: 'Hasil', page: 7 },
      ],
    }),
  )
  for (let i = 1; i <= 8; i++)
    writeFileSync(join(doc, `memo-${i}.txt`), `memo ${i}\nbutuh tindak lanjut\n`)
  if (existsSync(VIPS)) {
    execFileSync(VIPS, ['black', join(root, 'foto', 'pantai.jpg'), '1600', '1000'])
    writeFileSync(
      join(root, 'foto', 'pantai.jpg'),
      withExif(readFileSync(join(root, 'foto', 'pantai.jpg'))),
    )
    execFileSync(VIPS, ['black', join(root, 'foto', 'gunung.png'), '800', '600'])
  }
  if (existsSync(FFMPEG)) {
    const ff = (...a: string[]) => execFileSync(FFMPEG, ['-nostdin', '-v', 'error', '-y', ...a])
    ff(
      '-f',
      'lavfi',
      '-i',
      'sine=frequency=440:duration=30',
      '-c:a',
      'libmp3lame',
      '-b:a',
      '64k',
      '-id3v2_version',
      '3',
      '-metadata',
      'title=Senja di Pantai',
      '-metadata',
      'artist=Grup Uji',
      '-metadata',
      'album=Album Uji',
      join(root, 'musik', '01-senja.mp3'),
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
      '-metadata',
      'title=Pagi Cerah',
      join(root, 'musik', '02-pagi.mp3'),
    )
    ff(
      '-f',
      'lavfi',
      '-i',
      'testsrc=size=640x360:rate=25',
      '-f',
      'lavfi',
      '-i',
      'sine=frequency=300',
      '-t',
      '20',
      '-c:v',
      'libx264',
      '-pix_fmt',
      'yuv420p',
      '-c:a',
      'aac',
      '-shortest',
      '-movflags',
      '+faststart',
      join(root, 'video', 'klip.mp4'),
    )
    writeFileSync(
      join(root, 'video', 'klip.srt'),
      '1\n00:00:00,000 --> 00:00:19,000\nHalo, ini <i>subtitle</i> uji\n',
    )
  }
}

async function apiPut(page: Page, path: string, data: unknown, extra: Record<string, string> = {}) {
  const token = await csrfToken(page.request)
  return page.request.put(`/api/v1${path}`, {
    data,
    headers: { 'X-CSRF-Token': token, Origin: ORIGIN, ...extra },
  })
}

async function setTheme(page: Page, theme: 'light' | 'dark') {
  await page.evaluate((t) => localStorage.setItem('theme', t), theme)
}

async function shot(page: Page, name: string, theme: string) {
  mkdirSync(OUT, { recursive: true })
  await page.waitForTimeout(400)
  await page.screenshot({ path: join(OUT, `${name}-${theme}.png`), animations: 'disabled' })
}

const rowOf = (page: Page, name: string) =>
  page.locator('[data-testid="details-body"] [role="row"]').filter({ hasText: name }).first()

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(500)
  await apiResetPrefs(page.request)
  if (!seeded) {
    await apiMkdir(page.request, '/', 'ux')
    seed()
  }
})

for (const theme of ['light', 'dark'] as const) {
  test(`screenshots ${theme}`, async ({ page }) => {
    await setTheme(page, theme)
    // search: names, then content
    await page.goto(`/search?path=${encodeURIComponent(`${DIR}/dokumen`)}`)
    await page.getByTestId('search-input').fill('memo')
    await page.getByTestId('search-submit').click()
    await expect(page.getByTestId('search-name-row').first()).toBeVisible()
    await shot(page, 'search-names', theme)
    await page.getByTestId('search-tab-content').click()
    await page.getByTestId('search-input').fill('tindak lanjut')
    await page.getByTestId('search-submit').click()
    await expect(page.getByTestId('search-content-file').first()).toBeVisible()
    await shot(page, 'search-content', theme)

    // editor: three versions → history → diff; then a conflict
    const path = `${DIR}/dokumen/catatan.txt`
    for (const body of ['v2\n', 'v3\n', 'v4 isi terbaru\n']) {
      const r = await page.request.get(`/api/v1/files/content?path=${encodeURIComponent(path)}`)
      const etag = r.headers()['etag'] ?? ''
      await apiPut(page, '/files/content', { path, content: body }, { 'If-Match': etag })
    }
    await page.goto(`/editor?path=${encodeURIComponent(path)}`)
    await expect(page.locator('.monaco-editor .view-lines').first()).toBeVisible({
      timeout: 30_000,
    })
    await expect(page.getByTestId('status-history')).toHaveText(/Riwayat \(/)
    await page.getByTestId('status-history').click()
    await expect(page.getByTestId('version-history')).toBeVisible()
    await shot(page, 'editor-history', theme)
    await page
      .getByTestId('version-history')
      .getByTestId('version-row')
      .last()
      .getByTestId('version-compare')
      .click()
    await expect(page.getByTestId('diff-dialog').locator('.monaco-diff-editor')).toBeVisible({
      timeout: 15_000,
    })
    await page.waitForTimeout(800)
    await shot(page, 'editor-diff', theme)
    await page.keyboard.press('Escape')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('diff-dialog')).toHaveCount(0)
    // conflict: the server moves on, the editor saves with a stale etag
    await apiPut(page, '/files/content', { path, content: 'v5 dari sesi lain\n', force: true })
    await page.locator('.monaco-editor .view-lines').first().click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type('baris dari editor\n')
    await page.keyboard.press('Control+s')
    const conflict = page.getByTestId('conflict-dialog')
    await expect(conflict).toBeVisible()
    await expect(conflict.locator('.monaco-diff-editor')).toBeVisible({ timeout: 15_000 })
    await page.waitForTimeout(800)
    await shot(page, 'editor-conflict', theme)
    await page.keyboard.press('Escape')
    await apiPut(page, '/me/editor-session', { tabs: [], activePath: null })

    // image viewer + info panel
    if (existsSync(VIPS)) {
      await page.goto(`/files${DIR}/foto`)
      await rowOf(page, 'pantai.jpg').dblclick()
      await expect(page.getByTestId('viewer-image')).toBeVisible()
      await page.keyboard.press('i')
      await expect(page.getByTestId('viewer-exif')).toContainText('Canon')
      await shot(page, 'viewer-image-info', theme)
      await page.keyboard.press('Escape')
    }
    // PDF viewer with sidebar
    await page.goto(`/files${DIR}/dokumen`)
    await rowOf(page, 'laporan.pdf').dblclick()
    await expect(page.getByTestId('pdf-page-count')).toContainText('12', { timeout: 30_000 })
    await page.getByTestId('pdf-sidebar-toggle').click()
    await expect(page.getByTestId('pdf-thumb').first()).toBeVisible()
    await page.waitForTimeout(1500)
    await shot(page, 'viewer-pdf', theme)
    await page.keyboard.press('Escape')

    // CSV / JSON / Markdown / code viewers
    await rowOf(page, 'data.csv').dblclick()
    await expect(page.getByTestId('csv-viewer')).toContainText('Banyuwangi')
    await shot(page, 'viewer-csv', theme)
    await page.keyboard.press('Escape')
    await rowOf(page, 'config.json').dblclick()
    await expect(page.getByTestId('json-tree')).toContainText('aplikasi')
    await shot(page, 'viewer-json', theme)
    await page.keyboard.press('Escape')
    await rowOf(page, 'README.md').dblclick()
    await expect(page.getByTestId('markdown-rendered').locator('h1')).toBeVisible()
    await shot(page, 'viewer-markdown', theme)
    await page.keyboard.press('Escape')
    await rowOf(page, 'main.go').dblclick()
    await expect(page.getByTestId('code-viewer')).toBeVisible()
    await expect(page.locator('.hljs').first()).toBeVisible()
    await shot(page, 'viewer-code', theme)
    await page.keyboard.press('Escape')

    // Quick Look
    await rowOf(page, 'README.md').click()
    await page.keyboard.press('Space')
    await expect(page.getByTestId('quick-look')).toBeVisible()
    await page.waitForTimeout(500)
    await shot(page, 'quick-look', theme)
    await page.keyboard.press('Escape')

    if (existsSync(FFMPEG)) {
      // audio player → mini-player while browsing another folder
      await page.goto(`/files${DIR}/musik`)
      await rowOf(page, '01-senja.mp3').dblclick()
      await expect(page.getByTestId('player-dialog')).toBeVisible()
      await expect(page.getByTestId('audio-title')).toHaveText('Senja di Pantai')
      await page.waitForTimeout(1500)
      await shot(page, 'audio-player', theme)
      await page.keyboard.press('Escape')
      // in-app navigation (a full reload would reset the in-memory player)
      await page.getByTestId(`crumb-${DIR}`).click()
      await rowOf(page, 'dokumen').dblclick()
      await expect(page).toHaveURL(new RegExp(`/files${DIR}/dokumen$`))
      await expect(page.getByTestId('mini-player')).toBeVisible()
      await shot(page, 'mini-player', theme)
      await page.getByTestId('mini-player').getByTestId('mini-player-close').click()
      // video with subtitle
      await page.goto(`/files${DIR}/video`)
      await rowOf(page, 'klip.mp4').dblclick()
      await expect(page.getByTestId('video-player')).toBeVisible({ timeout: 30_000 })
      await page.getByTestId('video-element').evaluate((v) => (v as HTMLVideoElement).play())
      await page.waitForTimeout(2500)
      await shot(page, 'video-subtitle', theme)
      await page.keyboard.press('Escape')
    }

    // 390 px: browser, viewer, search
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(`/files${DIR}/dokumen`)
    await expect(page.getByTestId('details-grid')).toBeVisible()
    await shot(page, 'mobile-files', theme)
    await rowOf(page, 'README.md').dblclick()
    await expect(page.getByTestId('markdown-rendered')).toBeVisible()
    await page.keyboard.press('i')
    await page.waitForTimeout(300)
    await shot(page, 'mobile-viewer-info', theme)
    await page.keyboard.press('Escape')
    await page.goto(`/search?path=${encodeURIComponent(`${DIR}/dokumen`)}&q=memo`)
    await page.getByTestId('search-submit').click()
    await expect(page.getByTestId('search-name-row').first()).toBeVisible()
    await shot(page, 'mobile-search', theme)
    await page.setViewportSize({ width: 1280, height: 720 })
  })
}
