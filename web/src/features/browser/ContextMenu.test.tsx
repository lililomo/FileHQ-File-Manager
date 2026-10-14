import { configure, fireEvent, render, screen } from '@testing-library/react'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import type { Item } from '@/features/browser/api'
import {
  ACTIONS,
  MENU_MATRIX,
  actionById,
  menuContextFor,
  menuItems,
  type ActionContext,
} from '@/lib/actions'
import { BrowserContextMenu } from './ContextMenu'

configure({ asyncUtilTimeout: 8000 })
beforeAll(() => i18n.changeLanguage('id'))

function item(path: string, isDir = false): Item {
  return {
    name: path.split('/').pop()!,
    path,
    type: isDir ? 'dir' : 'file',
    isDir,
    isSymlink: false,
    size: 10,
    itemCount: isDir ? 0 : null,
    mime: isDir ? null : 'text/plain',
    typeLabel: isDir ? 'type.folder' : 'type.text.plain',
    category: isDir ? 'folder' : 'text',
    ext: isDir ? '' : 'txt',
    mtime: '2026-10-14T00:00:00Z',
    ctime: '2026-10-14T00:00:00Z',
    btime: null,
    atime: '2026-10-14T00:00:00Z',
    mode: '-rw-r--r--',
    modeOctal: '0644',
    note: null,
    tag: null,
    favorite: false,
    pinned: false,
    shared: 'none',
    media: null,
    thumb: false,
    etag: 'x',
  }
}

function ctxWith(selection: Item[], extra: Partial<ActionContext> = {}): ActionContext {
  const fn = vi.fn()
  return {
    path: '/',
    selection,
    selectionCount: selection.length,
    total: 5,
    clipboardCount: 0,
    canUndo: false,
    navigate: fn,
    refresh: fn,
    openNew: fn,
    openTab: fn,
    closeTab: fn,
    togglePreview: fn,
    toggleTheme: fn,
    selectAll: fn,
    invertSelection: fn,
    clearSelection: fn,
    openHelp: fn,
    openColumns: fn,
    openProperties: fn,
    openFolderProperties: fn,
    quickLook: fn,
    focusFilter: fn,
    open: fn,
    openInNewTab: fn,
    copy: fn,
    cut: fn,
    paste: fn,
    duplicate: fn,
    rename: fn,
    moveTo: fn,
    copyTo: fn,
    deleteItems: fn,
    deletePermanent: fn,
    batchRename: fn,
    permissions: fn,
    symlinkTo: fn,
    copyPath: fn,
    undo: fn,
    ...extra,
  }
}

const ids = (groups: ReturnType<typeof menuItems>) => groups.map((g) => g.map((a) => a.id))

describe('context-menu matrix (SPEC §9.5)', () => {
  it('every id in the matrix is a registered action', () => {
    for (const list of Object.values(MENU_MATRIX)) {
      for (const id of list) if (id !== '|') expect(actionById(id), id).toBeDefined()
    }
  })

  it('1 file: shows the 9b/10a actions, hides unavailable ones, Delete last', () => {
    const groups = ids(menuItems('file', ctxWith([item('/a.txt')])))
    expect(groups).toEqual([
      ['open', 'quickLook'],
      ['download'],
      ['copy', 'cut', 'duplicate', 'rename', 'moveTo', 'copyTo'],
      ['compress'], // Step 14a (extract/browseArchive only for archives)
      ['permissions', 'symlink', 'checksum'],
      ['copyPath', 'properties'],
      ['delete'],
    ])
    const flat = groups.flat()
    // Step 14a: compress is live; share/versions still wait for their steps
    for (const hidden of ['share', 'favorite', 'versions']) expect(flat).not.toContain(hidden)
    expect(flat[flat.length - 1]).toBe('delete')
    expect(actionById('delete')!.destructive).toBe(true)
  })

  it('1 folder: open in new tab instead of Quick Look, folder size available (10a)', () => {
    const groups = ids(menuItems('folder', ctxWith([item('/dir', true)])))
    expect(groups[0]).toEqual(['open', 'openInNewTab', 'downloadZip'])
    expect(groups.flat()).not.toContain('quickLook')
    expect(groups.flat()).toContain('rename')
    expect(groups.flat()).toContain('folderSize')
    expect(groups.flat()).not.toContain('checksum')
    expect(groups.flat().at(-1)).toBe('delete')
  })

  it('Recycle Bin items (10a): restore, restore to, purge last and destructive', () => {
    const fn = vi.fn()
    const groups = ids(
      menuItems('trash', ctxWith([], { selectionCount: 2, restore: fn, restoreTo: fn, purge: fn })),
    )
    expect(groups).toEqual([['restore', 'restoreTo'], ['purge']])
    expect(actionById('purge')!.destructive).toBe(true)
    actionById('restore')!.run(ctxWith([], { selectionCount: 1, restore: fn }))
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('many items: batch rename, no rename/duplicate/properties', () => {
    const groups = ids(menuItems('multi', ctxWith([item('/a.txt'), item('/b.txt')])))
    expect(groups).toEqual([
      ['downloadZip'],
      ['copy', 'cut', 'moveTo', 'copyTo'],
      ['compress', 'batchRename', 'permissions'],
      ['delete'],
    ])
  })

  it('background: Baru submenu, paste only with a clipboard, view/sort/columns, folder properties', () => {
    const empty = ids(menuItems('background', ctxWith([])))
    // Step 12a: upload actions are live; remote download (Step 13) follows the config flag
    expect(empty).toEqual([
      ['new', 'uploadFile', 'uploadFolder', 'remoteDownload'],
      ['refresh', 'selectAll'],
      ['view', 'sort', 'columns'],
      ['folderProperties'],
    ])
    const withClip = ids(menuItems('background', ctxWith([], { clipboardCount: 2 })))
    expect(withClip[1]).toEqual(['paste', 'refresh', 'selectAll'])
    const noRemote = ids(menuItems('background', ctxWith([], { remoteDownloadEnabled: false })))
    expect(noRemote[0]).toEqual(['new', 'uploadFile', 'uploadFolder'])
  })

  it('menuContextFor picks the context from the selection', () => {
    expect(menuContextFor({ selection: [], selectionCount: 0 })).toBe('background')
    expect(menuContextFor({ selection: [item('/a')], selectionCount: 1 })).toBe('file')
    expect(menuContextFor({ selection: [item('/d', true)], selectionCount: 1 })).toBe('folder')
    expect(menuContextFor({ selection: [item('/a')], selectionCount: 3 })).toBe('multi')
  })

  it('unavailable actions never reach the flat surfaces either', () => {
    const unavailable = ACTIONS.filter((a) => a.available === false).map((a) => a.id)
    expect(unavailable).toContain('share')
    for (const context of ['file', 'folder', 'multi', 'background', 'trash', 'archive'] as const) {
      const flat = ids(
        menuItems(context, ctxWith([item('/a.txt'), item('/b', true)], { selectionCount: 2 })),
      ).flat()
      for (const id of unavailable) expect(flat).not.toContain(id)
    }
  })

  it('archive browser context (Step 14b): extract, download one, remove only for a modifiable zip', () => {
    const fn = vi.fn()
    const base = { extractEntries: fn, downloadEntry: fn, removeEntries: fn }
    expect(
      ids(
        menuItems('archive', ctxWith([], { ...base, selectionCount: 1, archiveCanModify: true })),
      ),
    ).toEqual([['extractEntries', 'downloadEntry'], ['removeEntries']])
    // several members: no single-entry download; read-only container: no remove
    expect(
      ids(
        menuItems('archive', ctxWith([], { ...base, selectionCount: 2, archiveCanModify: false })),
      ),
    ).toEqual([['extractEntries']])
    // nothing selected → nothing (the overlay shows no background menu)
    expect(ids(menuItems('archive', ctxWith([], { ...base, selectionCount: 0 })))).toEqual([])
  })
})

describe('<BrowserContextMenu>', () => {
  it('renders the file context on right-click and runs an action', async () => {
    const ctx = ctxWith([item('/a.txt')])
    render(
      <BrowserContextMenu
        ctx={ctx}
        context="file"
        viewMenu={null}
        sortMenu={null}
        columnsMenu={null}
        templates={[]}
      >
        <div data-testid="area">area</div>
      </BrowserContextMenu>,
    )
    fireEvent.contextMenu(screen.getByTestId('area'))
    const menu = await screen.findByTestId('context-menu')
    expect(menu).toHaveAttribute('data-context', 'file')
    expect(screen.getByTestId('cm-rename')).toHaveTextContent('Rename')
    expect(screen.getByTestId('cm-delete')).toHaveAttribute('data-variant', 'destructive')
    expect(screen.queryByTestId('cm-share')).not.toBeInTheDocument()
    expect(screen.queryByTestId('cm-batchRename')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('cm-rename'))
    expect(ctx.rename).toHaveBeenCalled()
  })
})
