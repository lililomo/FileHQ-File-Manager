import { expect, test, type Page } from '@playwright/test'
import { existsSync, mkdirSync, unlinkSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { ADMIN_USERNAME, apiResetPrefs, e2eStorageRoot, loginAsAdmin } from './fixtures'

// Step 21 frontend targets (PRD G3/G4, SPEC §11.5, PROMPT 21 AC): first render of a
// 10 000-entry folder < 1 s, warm navigation < 150 ms, no long task > 100 ms while
// scrolling (goal < 50 ms, reported). Timings are taken inside the page with
// performance.mark/measure so Playwright round trips are not counted.
const LIMIT = { firstRender: 1000, warmNav: 150, longTask: 100, longTaskGoal: 50 }
const DIR = 'perf10k'
const FILES = 10_000
const FOLDERS = 500
const EXTS = ['txt', 'jpg', 'png', 'pdf', 'md', 'go', 'zip', 'mp4', 'mp3', 'csv', 'docx', 'json']

test.describe.configure({ mode: 'serial', timeout: 180_000 })
// Playwright tracing snapshots the DOM after every action inside the page and
// would be measured as the app's own long tasks (seen in the Step 21 profile)
test.use({ trace: 'off' })

const nameOf = (i: number) => {
  const ext = EXTS[i % EXTS.length]!
  return i % 97 === 0 ? `Laporan ${i} (final).${ext}` : `file${i}.${ext}`
}

function seed() {
  const big = join(e2eStorageRoot(), ADMIN_USERNAME, DIR)
  if (existsSync(join(big, 'folder-1'))) return
  mkdirSync(big, { recursive: true })
  for (let i = 1; i <= FILES; i++) {
    writeFileSync(join(big, nameOf(i)), i % 7 === 0 ? `isi ${i}` : '')
  }
  for (let i = 1; i <= FOLDERS; i++) {
    mkdirSync(join(big, `folder-${i}`))
    if (i <= 3) writeFileSync(join(big, `folder-${i}`, 'catatan.txt'), 'x')
  }
}

/** Starts a long-task observer in the page (buffered, so nothing is missed). */
async function watchLongTasks(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __longTasks: number[]; __po?: PerformanceObserver }
    w.__po?.disconnect()
    w.__longTasks = []
    w.__po = new PerformanceObserver((list) => {
      for (const e of list.getEntries()) w.__longTasks.push(Math.round(e.duration))
    })
    w.__po.observe({ type: 'longtask' })
  })
}

async function longTasks(page: Page): Promise<number[]> {
  return page.evaluate(() => (window as unknown as { __longTasks: number[] }).__longTasks.slice())
}

/** Arms a measure named `name`: it starts at the next trusted click/dblclick
 *  matching `trigger` and ends on the frame after `selector` is in the DOM. */
async function armMeasure(
  page: Page,
  name: string,
  trigger: 'click' | 'dblclick',
  selector: string,
) {
  await page.evaluate(
    ([n, ev, sel]) => {
      performance.clearMarks(n)
      performance.clearMeasures(n)
      const start = () => {
        performance.mark(`${n}:start`)
        const done = () => {
          requestAnimationFrame(() => setTimeout(() => performance.measure(n, `${n}:start`), 0))
        }
        if (document.querySelector(sel)) {
          done()
          return
        }
        const mo = new MutationObserver(() => {
          if (document.querySelector(sel)) {
            mo.disconnect()
            done()
          }
        })
        mo.observe(document.body, { childList: true, subtree: true })
      }
      document.addEventListener(ev, start, { capture: true, once: true })
    },
    [name, trigger, selector] as const,
  )
}

async function measured(page: Page, name: string): Promise<number> {
  await expect
    .poll(() => page.evaluate((n) => performance.getEntriesByName(n, 'measure').length, name))
    .toBe(1)
  return page.evaluate(
    (n) => Math.round(performance.getEntriesByName(n, 'measure')[0]!.duration),
    name,
  )
}

test.beforeAll(() => seed())

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await apiResetPrefs(page.request)
})

test('10,000 entries: first render < 1 s, warm navigation < 150 ms', async ({ page }) => {
  await page.goto('/files')
  const folderRow = page.locator(`[data-path="/${DIR}"]`)
  await expect(folderRow).toBeVisible()
  await watchLongTasks(page)

  // cold: the server cache for this folder is invalidated by touching it
  const touch = join(e2eStorageRoot(), ADMIN_USERNAME, DIR, '.touch')
  writeFileSync(touch, String(Date.now()))
  unlinkSync(touch)
  await armMeasure(page, 'first-rows', 'dblclick', `[data-path="/${DIR}/folder-1"]`)
  await folderRow.dblclick()
  const first = await measured(page, 'first-rows')
  await expect(page.getByTestId('status-items')).toHaveText('10.500 item')
  const firstTasks = await longTasks(page)
  console.log(
    `[perf] first render 10.500 entries: ${first} ms; long tasks: ${firstTasks.join(',') || '-'}`,
  )

  // warm: into a sub folder and back (cached query → rows from memory, then revalidate)
  const sub = page.locator(`[data-path="/${DIR}/folder-1"]`)
  await sub.dblclick()
  await expect(page.locator(`[data-path="/${DIR}/folder-1/catatan.txt"]`)).toBeVisible()
  const backs: number[] = []
  for (let i = 0; i < 5; i++) {
    await armMeasure(page, `back-${i}`, 'click', `[data-path="/${DIR}/folder-1"]`)
    await page.getByTestId('nav-back').click()
    backs.push(await measured(page, `back-${i}`))
    await expect(page.getByTestId('status-items')).toHaveText('10.500 item')
    await armMeasure(page, `fwd-${i}`, 'click', `[data-path="/${DIR}/folder-1/catatan.txt"]`)
    await page.getByTestId('nav-forward').click()
    backs.push(await measured(page, `fwd-${i}`))
  }
  backs.sort((a, b) => a - b)
  const p95 = backs[Math.ceil(backs.length * 0.95) - 1]!
  console.log(
    `[perf] warm navigation (back/forward, n=${backs.length}): ${backs.join(',')} ms; p95 ${p95}`,
  )
  expect(first).toBeLessThan(LIMIT.firstRender)
  expect(p95).toBeLessThan(LIMIT.warmNav)
})

test('scrolling 10,000 entries: no long task > 100 ms', async ({ page }) => {
  await page.goto(`/files/${DIR}`)
  const grid = page.getByTestId('details-grid')
  await expect(page.getByTestId('status-items')).toHaveText('10.500 item')
  await expect(page.locator(`[data-path="/${DIR}/folder-1"]`)).toBeVisible()
  await watchLongTasks(page)
  const rowsInDom = await page.locator('[data-testid="details-body"] [role="row"]').count()
  expect(rowsInDom).toBeLessThan(80)

  // scroll like a user flinging the wheel (2 viewport heights per frame); infinite scroll
  // fetches the remaining 20 pages as the end of the loaded rows comes near
  // natural order: folders, file1 … file10000, then "Laporan …" (L after f); the last
  // entry is the highest multiple of 97
  const last = page.locator(`[data-path="/${DIR}/${nameOf(9991)}"]`)
  const t0 = Date.now()
  for (let i = 0; i < 1500 && (await last.count()) === 0; i++) {
    await grid.evaluate((el) => el.scrollBy(0, el.clientHeight * 2))
    await page.waitForTimeout(16)
  }
  await expect(last).toBeVisible({ timeout: 30_000 })
  const tasks = await longTasks(page)
  const max = tasks.length ? Math.max(...tasks) : 0
  console.log(
    `[perf] scroll through 10.500 rows in ${Date.now() - t0} ms: ${tasks.length} long tasks (max ${max} ms, > ${LIMIT.longTaskGoal} ms: ${tasks.filter((d) => d > LIMIT.longTaskGoal).length}): ${tasks.join(',') || '-'}`,
  )
  expect(max).toBeLessThanOrEqual(LIMIT.longTask)
})

test('sorting 10,000 entries (name descending) is served by the server < 300 ms', async ({
  page,
}) => {
  await page.goto(`/files/${DIR}`)
  await expect(page.getByTestId('status-items')).toHaveText('10.500 item')
  await expect(page.locator(`[data-path="/${DIR}/folder-1"]`)).toBeVisible()
  await watchLongTasks(page)
  // folders stay first; descending natural order puts folder-500 on top
  await armMeasure(page, 'sort', 'click', `[data-index="0"][data-path="/${DIR}/folder-500"]`)
  await page.getByTestId('col-header-name').click()
  const sortMs = await measured(page, 'sort')
  await expect(page.getByTestId('col-header-name')).toHaveAttribute('data-sort', 'desc')
  console.log(
    `[perf] sort name desc (cold server sort + first rows): ${sortMs} ms; long tasks ${(await longTasks(page)).join(',') || '-'}`,
  )
  expect(sortMs).toBeLessThan(300)
})
