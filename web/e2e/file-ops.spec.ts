import { expect, test, type Locator, type Page } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import {
  apiCreateFile,
  apiMkdir,
  apiResetPrefs,
  csrfToken,
  e2eStorageRoot,
  loginAsAdmin,
} from './fixtures'

test.describe.configure({ mode: 'serial', timeout: 120_000 })

const DIR = '/ops'
let seeded = false

async function setPrefs(page: Page, prefs: Record<string, unknown>) {
  const token = await csrfToken(page.request)
  const res = await page.request.put('/api/v1/me/preferences', {
    data: { prefs },
    headers: { 'X-CSRF-Token': token, Origin: 'http://127.0.0.1:18090' },
  })
  expect(res.status(), await res.text()).toBe(200)
}

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
  await page.waitForTimeout(700)
  await apiResetPrefs(page.request)
  await setPrefs(page, { 'view.mode': null, 'fileops.confirm_delete': null })
  if (!seeded) {
    seeded = true
    await apiMkdir(page.request, '/', 'ops')
    for (const n of ['a', 'b', 'c', 'd', 'e']) await apiCreateFile(page.request, DIR, `${n}.txt`, n)
    await apiMkdir(page.request, DIR, 'tujuan')
    await apiMkdir(page.request, DIR, 'lain')
    await apiMkdir(page.request, DIR, 'konflik')
    await apiMkdir(page.request, DIR, 'konflik2')
    for (const n of ['x1', 'x2', 'x3']) {
      await apiCreateFile(page.request, `${DIR}/konflik`, `${n}.txt`, 'x')
      await apiCreateFile(page.request, `${DIR}/konflik2`, `${n}.txt`, 'yy')
    }
    const banyak = join(e2eStorageRoot(), 'admin', 'ops', 'banyak')
    mkdirSync(banyak, { recursive: true })
    for (let i = 1; i <= 51; i++)
      writeFileSync(join(banyak, `f${String(i).padStart(2, '0')}.txt`), 'z')
  }
})

const row = (page: Page, path: string) =>
  page.locator(`[data-testid="details-body"] [role="row"][data-path="${path}"]`)

/** Arrow down through an open Radix menu until `testId` has focus. */
async function arrowTo(page: Page, testId: string) {
  const target = page.getByTestId(testId)
  for (let i = 0; i < 12; i++) {
    if (await target.evaluate((el) => el === document.activeElement)) return
    await page.keyboard.press('ArrowDown')
  }
  await expect(target).toBeFocused()
}

/** Toast text (scoped to the sonner region: the live region repeats the message). */
const toast = (page: Page, text: string | RegExp) =>
  page.locator('[data-sonner-toaster]').getByText(text)

async function openDir(page: Page, path: string, count: number) {
  await page.goto(`/files${path}`)
  await expect(page.getByTestId('details-grid')).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText(`${count} item`)
}

/** Synthetic HTML5 drag between two elements (lets us hold Ctrl, which
 *  page.dragAndDrop cannot). */
async function dragWithModifier(page: Page, src: Locator, dst: Locator, ctrlKey: boolean) {
  const dt = await page.evaluateHandle(() => new DataTransfer())
  await src.dispatchEvent('dragstart', { dataTransfer: dt })
  await dst.dispatchEvent('dragenter', { dataTransfer: dt, ctrlKey })
  await dst.dispatchEvent('dragover', { dataTransfer: dt, ctrlKey })
  await dst.dispatchEvent('drop', { dataTransfer: dt, ctrlKey })
  // the source row may already be gone (optimistic move): dragend is best effort
  await src.dispatchEvent('dragend', { dataTransfer: dt }, { timeout: 1500 }).catch(() => {})
}

test('context menu shows exactly the SPEC §9.5 actions per context', async ({ page }) => {
  await openDir(page, DIR, 10)
  const menu = page.getByTestId('context-menu')

  // 1 file
  await row(page, '/ops/a.txt').click({ button: 'right' })
  await expect(menu).toHaveAttribute('data-context', 'file')
  for (const id of [
    'open',
    'quickLook',
    'copy',
    'cut',
    'duplicate',
    'rename',
    'moveTo',
    'copyTo',
    'permissions',
    'symlink',
    'checksum',
    'copyPath',
    'properties',
    'delete',
    'download', // Step 12b
  ])
    await expect(page.getByTestId(`cm-${id}`), id).toBeVisible()
  for (const id of [
    'share',
    'compress',
    'versions',
    'batchRename',
    'openInNewTab',
    'new',
    'folderSize',
  ])
    await expect(page.getByTestId(`cm-${id}`), id).toHaveCount(0)
  const items = await menu
    .locator('[data-action]')
    .evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.action))
  expect(items[items.length - 1]).toBe('delete')
  await page.keyboard.press('Escape')
  await expect(menu).toHaveCount(0)

  // 1 folder
  await row(page, '/ops/tujuan').click({ button: 'right' })
  await expect(menu).toHaveAttribute('data-context', 'folder')
  await expect(page.getByTestId('cm-openInNewTab')).toBeVisible()
  await expect(page.getByTestId('cm-quickLook')).toHaveCount(0)
  await expect(page.getByTestId('cm-rename')).toBeVisible()
  await expect(page.getByTestId('cm-folderSize')).toBeVisible()
  await expect(page.getByTestId('cm-checksum')).toHaveCount(0)
  await page.keyboard.press('Escape')

  // many items (right-click inside the selection keeps it)
  await row(page, '/ops/a.txt').click()
  await row(page, '/ops/b.txt').click({ modifiers: ['ControlOrMeta'] })
  await row(page, '/ops/b.txt').click({ button: 'right' })
  await expect(menu).toHaveAttribute('data-context', 'multi')
  await expect(page.getByTestId('cm-batchRename')).toBeVisible()
  await expect(page.getByTestId('cm-rename')).toHaveCount(0)
  await expect(page.getByTestId('cm-duplicate')).toHaveCount(0)
  await expect(page.getByTestId('status-selected')).toContainText('2 dipilih')
  await page.keyboard.press('Escape')

  // empty area: below the last row
  const grid = page.getByTestId('details-grid')
  const box = (await grid.boundingBox())!
  await grid.click({ button: 'right', position: { x: box.width / 2, y: box.height - 12 } })
  await expect(menu).toHaveAttribute('data-context', 'background')
  for (const id of ['new', 'refresh', 'selectAll', 'view', 'sort', 'columns', 'folderProperties'])
    await expect(page.getByTestId(`cm-${id}`), id).toBeVisible()
  await expect(page.getByTestId('cm-paste')).toHaveCount(0) // clipboard empty
  await expect(page.getByTestId('cm-delete')).toHaveCount(0)
  await page.keyboard.press('Escape')
})

test('clipboard: Ctrl+C in tab 1, Ctrl+V in tab 2 copies; cut fades the item until pasted', async ({
  page,
}) => {
  await openDir(page, DIR, 10)
  await row(page, '/ops/a.txt').click()
  await page.keyboard.press('ControlOrMeta+c')
  // a second folder tab, navigated to the destination
  await page.keyboard.press('Alt+t')
  await expect(page.getByTestId('tab-bar').locator('[role="tab"]')).toHaveCount(2)
  await page.goto('/files/ops/tujuan')
  await expect(page.getByTestId('status-items')).toHaveText('0 item')
  await page.getByTestId('browser').click()
  await page.keyboard.press('ControlOrMeta+v')
  await expect(row(page, '/ops/tujuan/a.txt')).toBeVisible()
  await expect(toast(page, '1 item disalin')).toBeVisible()
  // paste again → conflict dialog (the copy is still on the clipboard)
  await page.keyboard.press('ControlOrMeta+v')
  await expect(page.getByTestId('conflict-dialog')).toBeVisible()
  await page.getByTestId('conflict-cancel').click()
  await expect(page.getByTestId('status-items')).toHaveText('1 item')

  // cut b.txt in /ops, paste into /ops/tujuan
  await openDir(page, DIR, 10)
  await row(page, '/ops/b.txt').click()
  await page.keyboard.press('ControlOrMeta+x')
  await expect(row(page, '/ops/b.txt')).toHaveAttribute('data-cut', 'true')
  await expect(page.getByTestId('toolbar-paste')).toBeEnabled()
  await openDir(page, '/ops/tujuan', 1)
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('ControlOrMeta+v')
  await expect(row(page, '/ops/tujuan/b.txt')).toBeVisible()
  await expect(toast(page, '1 item dipindahkan')).toBeVisible()
  await openDir(page, DIR, 9)
  await expect(row(page, '/ops/b.txt')).toHaveCount(0)
  // a cut clipboard is consumed by the paste
  await expect(page.getByTestId('toolbar-paste')).toBeDisabled()
})

test('conflict dialog compares both sides; "Terapkan ke semua" answers 3 conflicts at once', async ({
  page,
}) => {
  await openDir(page, '/ops/konflik', 3)
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.press('ControlOrMeta+c')
  await openDir(page, '/ops/konflik2', 3)
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('ControlOrMeta+v')
  const dialog = page.getByTestId('conflict-dialog')
  await expect(dialog).toBeVisible()
  await expect(page.getByTestId('conflict-counter')).toHaveText(/1 dari 3/)
  await expect(dialog.getByText('2 B')).toBeVisible() // existing (yy)
  await expect(dialog.getByText('1 B')).toBeVisible() // incoming (x)
  await page.getByTestId('conflict-apply-all').click()
  await page.getByTestId('conflict-rename').click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByTestId('status-items')).toHaveText('6 item')
  await expect(row(page, '/ops/konflik2/x1 (1).txt')).toBeVisible()
  await expect(row(page, '/ops/konflik2/x3 (1).txt')).toBeVisible()
  await expect(toast(page, '3 item disalin')).toBeVisible()
})

test('drag & drop: 2 files onto a folder move; with Ctrl they are copied', async ({ page }) => {
  await openDir(page, DIR, 9)
  await row(page, '/ops/c.txt').click()
  await row(page, '/ops/d.txt').click({ modifiers: ['ControlOrMeta'] })
  await page.dragAndDrop(
    '[data-testid="details-body"] [role="row"][data-path="/ops/c.txt"]',
    '[data-testid="details-body"] [role="row"][data-path="/ops/lain"]',
  )
  await expect(toast(page, '2 item dipindahkan')).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText('7 item')
  await expect(row(page, '/ops/c.txt')).toHaveCount(0)

  // Ctrl held while dropping = copy
  await row(page, '/ops/e.txt').click()
  await dragWithModifier(page, row(page, '/ops/e.txt'), row(page, '/ops/lain'), true)
  await expect(toast(page, '1 item disalin')).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText('7 item')
  await expect(row(page, '/ops/e.txt')).toBeVisible()
  await openDir(page, '/ops/lain', 3)
  for (const n of ['c', 'd', 'e']) await expect(row(page, `/ops/lain/${n}.txt`)).toBeVisible()

  // dropping onto a breadcrumb segment moves to that folder (c.txt no longer exists in /ops)
  await row(page, '/ops/lain/c.txt').click()
  await dragWithModifier(page, row(page, '/ops/lain/c.txt'), page.getByTestId('crumb-/ops'), false)
  await expect(toast(page, '1 item dipindahkan')).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText('2 item')
})

test('Delete asks (default preference), moves to the Recycle Bin, "Urungkan" restores', async ({
  page,
}) => {
  await openDir(page, DIR, 8)
  await row(page, '/ops/e.txt').click()
  await page.keyboard.press('Delete')
  await expect(page.getByTestId('delete-confirm')).toBeVisible()
  await page.getByTestId('delete-confirm-ok').click()
  await expect(row(page, '/ops/e.txt')).toHaveCount(0)
  await expect(page.getByTestId('status-items')).toHaveText('7 item')
  await expect(page.getByTestId('undo-toast')).toBeVisible()
  await page.getByTestId('undo-button').click()
  await expect(row(page, '/ops/e.txt')).toBeVisible()
  await expect(page.getByTestId('status-items')).toHaveText('8 item')
  await expect(toast(page, /Diurungkan/)).toBeVisible()

  // Ctrl+Z while the toast is visible undoes too
  await row(page, '/ops/e.txt').click()
  await page.keyboard.press('Delete')
  await page.getByTestId('delete-confirm-ok').click()
  await expect(row(page, '/ops/e.txt')).toHaveCount(0)
  await expect(page.getByTestId('undo-toast')).toBeVisible()
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('ControlOrMeta+z')
  await expect(row(page, '/ops/e.txt')).toBeVisible()
})

test('Shift+Delete on 51 items requires typing HAPUS', async ({ page }) => {
  await openDir(page, '/ops/banyak', 51)
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('ControlOrMeta+a')
  await expect(page.getByTestId('status-selected')).toContainText('51 dipilih')
  await page.keyboard.press('Shift+Delete')
  const dialog = page.getByTestId('delete-permanent')
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('51 item')
  const ok = page.getByTestId('delete-permanent-ok')
  await expect(ok).toBeDisabled()
  await page.getByTestId('delete-permanent-input').fill('hapus')
  await expect(ok).toBeDisabled()
  await page.getByTestId('delete-permanent-input').fill('HAPUS')
  await expect(ok).toBeEnabled()
  await ok.click()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText('Folder ini kosong')).toBeVisible()
  await expect(toast(page, '51 item dihapus permanen')).toBeVisible()
  // Step 10a: really gone, not in the Recycle Bin
  const trash = await page.request.get('/api/v1/trash?q=f01')
  expect(((await trash.json()) as { total: number }).total).toBe(0)
})

test('inline rename (F2) validates live; dialogs work keyboard-only and return focus to the grid', async ({
  page,
}) => {
  await openDir(page, DIR, 8)
  await row(page, '/ops/a.txt').click()
  await page.keyboard.press('F2')
  const input = page.getByTestId('inline-rename-input')
  await expect(input).toBeVisible()
  await expect(input).toBeFocused()
  // the stem is selected, not the extension
  expect(
    await input.evaluate((el: HTMLInputElement) => [el.selectionStart, el.selectionEnd]),
  ).toEqual([0, 1])
  await input.fill('a/b')
  await expect(page.getByTestId('inline-rename-hint')).toContainText('garis miring')
  await input.fill('e.txt')
  await expect(page.getByTestId('inline-rename-hint')).toContainText('Sudah ada')
  await page.keyboard.press('Escape')
  await expect(input).toHaveCount(0)
  await expect(row(page, '/ops/a.txt')).toBeVisible()
  await expect(page.getByTestId('details-grid')).toBeFocused()

  await page.keyboard.press('F2')
  await page.getByTestId('inline-rename-input').fill('aa.txt')
  await page.keyboard.press('Enter')
  await expect(row(page, '/ops/aa.txt')).toBeVisible()
  await expect(toast(page, 'Diganti nama menjadi aa.txt')).toBeVisible()
  await expect(page.getByTestId('details-grid')).toBeFocused()

  // Shift+F10 opens the menu for the selection; arrow to "Pindah ke…"
  await row(page, '/ops/aa.txt').click()
  await page.keyboard.press('Shift+F10')
  const menu = page.getByTestId('context-menu')
  await expect(menu).toHaveAttribute('data-context', 'file')
  await arrowTo(page, 'cm-moveTo')
  await page.keyboard.press('Enter')
  const move = page.getByTestId('movecopy-dialog')
  await expect(move).toBeVisible()
  await expect(page.getByTestId('folder-picker')).toBeFocused()
  await expect(page.getByTestId('movecopy-submit')).toBeDisabled() // same folder
  await page.keyboard.press('ArrowDown') // /ops → first child
  await expect(page.getByTestId('movecopy-submit')).toBeEnabled()
  await page.keyboard.press('Escape')
  await expect(move).toHaveCount(0)
  await expect(page.getByTestId('details-grid')).toBeFocused()

  // move via the dialog with the keyboard: pick /ops/tujuan and press Enter
  await page.keyboard.press('Shift+F10')
  await arrowTo(page, 'cm-moveTo')
  await page.keyboard.press('Enter')
  await expect(page.getByTestId('movecopy-dialog')).toBeVisible()
  await page.getByTestId('movecopy-path').fill('/ops/tujuan')
  await page.keyboard.press('Enter')
  await expect(page.getByTestId('movecopy-dialog')).toHaveCount(0)
  await expect(row(page, '/ops/aa.txt')).toHaveCount(0)
  await expect(toast(page, '1 item dipindahkan')).toBeVisible()
  await expect(page.getByTestId('details-grid')).toBeFocused()
})

test('batch rename: preview marks collisions, apply only when clean, undo restores', async ({
  page,
}) => {
  await openDir(page, '/ops/tujuan', 3) // a.txt, b.txt, aa.txt
  await page.getByTestId('details-grid').focus()
  await page.keyboard.press('ControlOrMeta+a')
  await page.getByTestId('toolbar-more').click()
  await page.getByTestId('toolbar-more-batchRename').click()
  const dialog = page.getByTestId('batch-rename-dialog')
  await expect(dialog).toBeVisible()
  const apply = page.getByTestId('br-apply')
  await expect(apply).toBeDisabled()
  // "a" → "b": a.txt→b.txt collides with b.txt
  await page.getByTestId('br-find').fill('a')
  await page.getByTestId('br-replace').fill('b')
  await expect(dialog.locator('[data-status="collision"]').first()).toBeVisible()
  await expect(apply).toBeDisabled()
  // numbering is clean: foto-1 … foto-3
  await page.getByTestId('br-find').fill('')
  await page.getByTestId('br-replace').fill('')
  await page.getByTestId('br-numbering').click()
  await page.getByTestId('br-pattern').fill('foto-{n}')
  await page.getByTestId('br-pad').fill('2')
  await expect(page.getByTestId('br-to-0')).toHaveText('foto-01.txt')
  await expect(apply).toBeEnabled()
  await apply.click()
  await expect(dialog).toHaveCount(0)
  await expect(row(page, '/ops/tujuan/foto-01.txt')).toBeVisible()
  await expect(row(page, '/ops/tujuan/foto-03.txt')).toBeVisible()
  await expect(page.getByTestId('undo-toast')).toBeVisible()
  await page.getByTestId('undo-button').click()
  await expect(row(page, '/ops/tujuan/a.txt')).toBeVisible()
  await expect(row(page, '/ops/tujuan/foto-01.txt')).toHaveCount(0)
})

test('"Baru": file from template and a symlink with a relative target; Permission dialog applies chmod', async ({
  page,
}) => {
  await openDir(page, '/ops/lain', 2) // d.txt, e.txt
  await page.getByTestId('toolbar-new').click()
  await page.getByTestId('toolbar-new-template').hover()
  await page.getByTestId('toolbar-template-md').click()
  const dlg = page.getByTestId('new-item-dialog')
  await expect(dlg).toBeVisible()
  await expect(page.getByTestId('new-item-name')).toHaveValue('baru.md')
  await page.getByTestId('new-item-name').fill('catatan.md')
  await page.getByTestId('new-item-submit').click()
  await expect(row(page, '/ops/lain/catatan.md')).toBeVisible()

  // symlink pointing at ../tujuan/b.txt
  await row(page, '/ops/lain/d.txt').click()
  await page.keyboard.press('Shift+F10')
  await page.getByTestId('cm-symlink').click()
  await expect(dlg).toBeVisible()
  await expect(page.getByTestId('new-item-target')).toHaveValue('/ops/lain/d.txt')
  await page.getByTestId('new-item-target').fill('/ops/tujuan/b.txt')
  await page.getByTestId('new-item-name').fill('tautan-b.txt')
  await expect(dlg).toContainText('../tujuan/b.txt')
  await page.getByTestId('new-item-submit').click()
  await expect(row(page, '/ops/lain/tautan-b.txt')).toBeVisible()
  await expect(toast(page, 'Symlink tautan-b.txt dibuat')).toBeVisible()

  // permissions: 0600 on e.txt
  await row(page, '/ops/lain/e.txt').click()
  await page.keyboard.press('Shift+F10')
  await page.getByTestId('cm-permissions').click()
  const perm = page.getByTestId('permission-dialog')
  await expect(perm).toBeVisible()
  await expect(page.getByTestId('perm-octal')).toHaveValue('0640') // files are created 0640
  await page.getByTestId('perm-group-r').click()
  await expect(page.getByTestId('perm-octal')).toHaveValue('0600')
  await page.getByTestId('perm-public-r').click()
  await expect(page.getByTestId('perm-octal')).toHaveValue('0604')
  await page.getByTestId('perm-octal').fill('0600')
  await expect(page.getByTestId('perm-public-r')).not.toBeChecked()
  await page.getByTestId('perm-submit').click()
  await expect(perm).toHaveCount(0)
  await expect(toast(page, /Permission 0600 diterapkan/)).toBeVisible()
  await expect(page.getByTestId('details-grid')).toBeFocused()
})
