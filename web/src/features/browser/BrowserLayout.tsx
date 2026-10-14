import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { FolderOpen, FolderPlus, Upload } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import {
  ContextMenuCheckboxItem,
  ContextMenuRadioGroup,
  ContextMenuRadioItem,
  ContextMenuSeparator,
} from '@/components/ui/context-menu'
import { Skeleton } from '@/components/ui/skeleton'
import { listTemplates } from '@/features/file-ops/api'
import { setDropHandlers, type DragPayload, parentOf } from '@/features/file-ops/dnd'
import { FileOpsDialogs } from '@/features/file-ops/FileOpsDialogs'
import { canUndoNow, focusGrid, useFileOpsDialogs, useUndoStore } from '@/features/file-ops/store'
import { useFileOps } from '@/features/file-ops/useFileOps'
import {
  menuContextFor,
  setActionContext,
  type ActionContext,
  type MenuContext,
} from '@/lib/actions'
import { announce } from '@/lib/announce'
import { ApiError } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { pushScope, registerShortcut } from '@/lib/shortcuts'
import { useIsMobile, useMediaQuery } from '@/lib/useMediaQuery'
import { cutPaths, useClipboardStore } from '@/stores/clipboard'
import { useOverlaysStore } from '@/stores/overlays'
import { getPref, usePref, usePrefsStore } from '@/stores/prefs'
import { useSelectionStore } from '@/stores/selection'
import { MAX_TABS, useTabsStore } from '@/stores/tabs'
import { useUIStore } from '@/stores/ui'
import { AddressBar } from './AddressBar'
import {
  getFolderPref,
  isViewMode,
  putFolderPref,
  statItem,
  VIEW_MODES,
  type Item,
  type SortField,
  type ViewMode,
} from './api'
import { ColumnContextMenuItems, ColumnMenu } from './ColumnMenu'
import { MOBILE_COLUMNS, normalizeColumns, orderedVisible, type ColumnsPref } from './columns'
import { BrowserContextMenu } from './ContextMenu'
import { DetailsView } from './DetailsView'
import { InlineRename } from './InlineRename'
import { PropertiesDialog, QuickLookDialog } from './ItemDialogs'
import { PreviewPanel } from './PreviewPanel'
import { QuickFilter } from './QuickFilter'
import {
  EMPTY_SELECTION,
  clear as clearSelection,
  invert as invertSelection,
  isSelected,
  selectAll,
  selectOnly,
  selectedCount,
  selectedItems,
  type SelectionState,
} from './SelectionModel'
import { SortMenu } from './SortMenu'
import { StatusBar } from './StatusBar'
import { TabBar } from './TabBar'
import { Toolbar } from './Toolbar'
import { ToolbarActions } from './ToolbarActions'
import { useListing } from './useListing'
import { ListView } from './views/ListView'
import { LargeIconsView, TilesView, XLIconsView } from './views/presets'
import { ViewSwitcher } from './views/ViewSwitcher'
import { baseName, parentPath, pathToSplat, ROOT } from './path'
import { cycleSort, normalizeSort, sameSort, type SortPref } from './sortCycle'

const MOBILE_PREF: ColumnsPref = { visible: MOBILE_COLUMNS, order: MOBILE_COLUMNS, widths: {} }
const SORT_FIELDS: SortField[] = [
  'name',
  'size',
  'type',
  'ext',
  'mtime',
  'btime',
  'atime',
  'mode',
  'note',
  'dimensions',
  'duration',
]

/** Pushes the "dialog" shortcut scope while an overlay is open. */
function useDialogScope(open: boolean): void {
  useEffect(() => {
    if (!open) return
    return pushScope('dialog')
  }, [open])
}

export function BrowserLayout({ path }: { path: string }) {
  const { t, i18n } = useTranslation(['files', 'fileops', 'errors', 'common'])
  const navigate = useNavigate()
  const qc = useQueryClient()
  const isMobile = useIsMobile()
  const previewOverlay = useMediaQuery('(max-width: 1279px)')
  const [editing, setEditing] = useState(false)
  const [quickLook, setQuickLook] = useState<Item | null>(null)
  const [properties, setProperties] = useState<Item | null>(null)
  const [columnsOpen, setColumnsOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [renaming, setRenaming] = useState<string | null>(null)
  const [menuContext, setMenuContext] = useState<MenuContext>('background')
  const [menuOpen, setMenuOpen] = useState(false)
  const filterRef = useRef<HTMLInputElement>(null)
  const openHelp = useOverlaysStore((s) => s.setHelp)
  const ops = useFileOps()
  const openDialog = useFileOpsDialogs((s) => s.open)
  const dialogOpen = useFileOpsDialogs((s) => s.dialog !== null)
  const clipboard = useClipboardStore((s) => s.data)
  const cut = useMemo(() => cutPaths(clipboard), [clipboard])
  const undoEntry = useUndoStore((s) => s.entry)
  const templates = useQuery({
    queryKey: ['templates'],
    queryFn: ({ signal }) => listTemplates(signal),
    staleTime: Infinity,
  })

  // --- tabs: URL is the source of truth; the store records history per tab ---
  const tabs = useTabsStore()
  useEffect(() => {
    useTabsStore.getState().visit(path)
  }, [path])
  const tab = tabs.active()
  const tabId = tab.id

  // --- preferences (server) ---
  const prefsLoaded = usePrefsStore((s) => s.loaded)
  const setPref = usePrefsStore((s) => s.set)
  const rawColumns = usePrefsStore((s) => s.prefs['view.columns'])
  const rawSort = usePrefsStore((s) => s.prefs['view.sort'])
  const globalColumns = useMemo(() => normalizeColumns(rawColumns), [rawColumns])
  const globalSort = useMemo(() => normalizeSort(rawSort), [rawSort])
  const foldersFirst = usePref<boolean>('view.folders_first', true)
  const rememberPerFolder = usePref<boolean>('view.remember_per_folder', true)
  const rawView = usePrefsStore((s) => s.prefs['view.mode'])
  const globalView: ViewMode = isViewMode(rawView) ? rawView : 'details'
  const previewOpen = usePref<boolean>('ui.preview_panel', false)
  const tabsRestore = usePref<boolean>('tabs.restore', true)

  const folderPref = useQuery({
    queryKey: ['folder-pref', path],
    queryFn: ({ signal }) => getFolderPref(path, signal),
    enabled: prefsLoaded && rememberPerFolder,
    staleTime: 60_000,
  })
  const sort: SortPref = useMemo(
    () => (folderPref.data ? normalizeSort(folderPref.data.sort) : globalSort),
    [folderPref.data, globalSort],
  )
  const columns: ColumnsPref = useMemo(
    () => (folderPref.data?.columns ? normalizeColumns(folderPref.data.columns) : globalColumns),
    [folderPref.data, globalColumns],
  )
  const view: ViewMode =
    folderPref.data && isViewMode(folderPref.data.view) ? folderPref.data.view : globalView

  const persistFolder = useCallback(
    (nextSort: SortPref, nextColumns: ColumnsPref, nextView: ViewMode) => {
      if (!rememberPerFolder) return
      const input = {
        path,
        view: nextView,
        sort: nextSort.field,
        dir: nextSort.dir,
        ...(nextSort.field2 ? { sort2: nextSort.field2, dir2: nextSort.dir2 ?? 'asc' } : {}),
        columns: {
          visible: nextColumns.visible,
          order: nextColumns.order,
          widths: nextColumns.widths as Record<string, number>,
        },
      }
      putFolderPref(input)
        .then((pref) => qc.setQueryData(['folder-pref', path], pref))
        .catch(() => {
          /* keep local state; the next change retries */
        })
      qc.setQueryData(['folder-pref', path], (old: unknown) => ({
        ...(old && typeof old === 'object' ? old : {}),
        path,
        view: nextView,
        sort: nextSort,
        columns: nextColumns,
        updatedAt: new Date().toISOString(),
      }))
    },
    [path, qc, rememberPerFolder],
  )
  const changeSort = useCallback(
    (next: SortPref) => {
      if (sameSort(next, sort)) return
      setPref('view.sort', next)
      persistFolder(next, columns, view)
    },
    [sort, columns, view, setPref, persistFolder],
  )
  const changeColumns = useCallback(
    (next: ColumnsPref) => {
      setPref('view.columns', next)
      persistFolder(sort, next, view)
    },
    [sort, view, setPref, persistFolder],
  )
  const changeView = useCallback(
    (next: ViewMode) => {
      if (next === view) return
      setPref('view.mode', next)
      persistFolder(sort, columns, next)
      announce(t(`files:view.${next}`))
    },
    [view, sort, columns, setPref, persistFolder, t],
  )
  const onHeaderSort = useCallback(
    (field: SortField, secondary: boolean) => changeSort(cycleSort(sort, field, secondary)),
    [sort, changeSort],
  )

  // --- restore / persist open tabs ---
  const restored = useRef(false)
  useEffect(() => {
    if (!prefsLoaded || restored.current) return
    restored.current = true
    const saved = getPref<{ path: string }[] | undefined>('tabs.open', undefined)
    if (tabsRestore && Array.isArray(saved) && saved.length > 0) {
      useTabsStore.getState().hydrate(
        saved.map((s) => s.path),
        path,
      )
    }
  }, [prefsLoaded, tabsRestore, path])
  const tabPaths = tabs.tabs.map((x) => x.path).join('\n')
  useEffect(() => {
    if (!restored.current) return
    // read the live store: in the hydration commit the rendered `tabPaths` is stale
    const live = useTabsStore.getState().tabs.map((x) => ({ path: x.path }))
    setPref('tabs.open', live)
  }, [tabPaths, setPref])

  // --- listing ---
  // Dimensi/Durasi columns ask the server to probe media metadata (SPEC §8.4).
  const wantMedia =
    view === 'details' &&
    !isMobile &&
    (columns.visible.includes('dimensions') || columns.visible.includes('duration'))
  const listing = useListing(path, {
    sort: sort.field,
    dir: sort.dir,
    sort2: sort.field2,
    dir2: sort.dir2,
    foldersFirst,
    media: wantMedia,
  })
  const { items, total, isPending, isError, error, refetch, isFetching } = listing
  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return items
    return items.filter((it) => it.name.toLowerCase().includes(q))
  }, [items, filter])

  // --- selection (per tab, cleared on navigation) ---
  const selection = useSelectionStore((s) => s.byTab[tabId]) ?? EMPTY_SELECTION
  const setSelection = useCallback(
    (next: SelectionState) => useSelectionStore.getState().set(tabId, next),
    [tabId],
  )
  // Reset per-folder UI state when the folder changes (render-time reset, no effect).
  const [seenPath, setSeenPath] = useState(path)
  if (seenPath !== path) {
    setSeenPath(path)
    setActiveIndex(0)
    setFilter('')
    setRenaming(null)
  }
  useEffect(() => {
    useSelectionStore.getState().clear(tabId)
  }, [path, tabId])
  const selCount = selectedCount(selection, items.length, total)
  const selItems = useMemo(() => selectedItems(selection, items), [selection, items])
  const selPaths = useMemo(() => selItems.map((it) => it.path), [selItems])
  const selBytes = selItems.reduce((n, it) => n + (it.isDir ? 0 : it.size), 0)
  const activeItem = filtered[Math.min(activeIndex, filtered.length - 1)] ?? null
  const focusItem = selItems.length === 1 ? selItems[0]! : activeItem
  const dragPaths = useCallback(
    (item: Item) => (isSelected(selection, item.path) ? selPaths : [item.path]),
    [selection, selPaths],
  )

  // --- navigation ---
  const go = useCallback(
    (to: string) => void navigate({ to: '/files/$', params: { _splat: pathToSplat(to) } }),
    [navigate],
  )
  const parent = parentPath(path)
  const handlers = useMemo(
    () => ({
      back: () => {
        const p = useTabsStore.getState().back()
        if (p !== null) go(p)
      },
      forward: () => {
        const p = useTabsStore.getState().forward()
        if (p !== null) go(p)
      },
      up: () => {
        if (parent !== null) go(parent)
      },
      refresh: () => {
        void refetch().then(() => announce(t('files:announce.refreshed')))
      },
      editPath: () => setEditing(true),
    }),
    [go, parent, refetch, t],
  )

  const openTab = useCallback(
    (p: string) => {
      const created = useTabsStore.getState().openTab(p)
      if (!created) {
        toast.error(t('files:tabs.limit', { max: MAX_TABS }))
        return
      }
      go(p)
    },
    [go, t],
  )
  const closeTab = useCallback(
    (id: string) => {
      const next = useTabsStore.getState().closeTab(id)
      useSelectionStore.getState().clear(id)
      if (next !== path) go(next)
    },
    [go, path],
  )
  const activateTab = useCallback(
    (id: string) => {
      const next = useTabsStore.getState().activate(id)
      if (next !== path) go(next)
    },
    [go, path],
  )

  const togglePreview = useCallback(
    () => setPref('ui.preview_panel', !getPref('ui.preview_panel', false)),
    [setPref],
  )

  const onOpen = useCallback(
    (item: Item) => {
      if (item.isDir || (item.isSymlink && !item.broken && item.type === 'dir')) {
        go(item.path)
        return
      }
      toast(t('files:viewer.notYet', { name: item.name }))
    },
    [go, t],
  )

  // --- file operations (Step 9b) ---
  const openNew = useCallback(
    (kind: 'folder' | 'file' | 'template' | 'symlink', opts?: { template?: string }) =>
      openDialog({
        kind: 'new',
        mode: kind,
        dir: path,
        ...(opts?.template ? { template: opts.template } : {}),
      }),
    [openDialog, path],
  )
  const pasteTarget = useCallback(() => {
    // paste into the single selected folder, else into the current folder
    if (selItems.length === 1 && selItems[0]!.isDir) return selItems[0]!.path
    return path
  }, [selItems, path])
  const fileOps = useMemo(
    () => ({
      copy: () => ops.copy(selPaths, path, tabId),
      cut: () => ops.cut(selPaths, path, tabId),
      paste: () => void ops.paste(pasteTarget()),
      duplicate: () => {
        if (focusItem) void ops.duplicate(focusItem.path)
      },
      rename: () => {
        if (focusItem) setRenaming(focusItem.path)
      },
      moveTo: () => {
        if (selPaths.length)
          openDialog({ kind: 'moveCopy', op: 'move', paths: selPaths, from: path })
      },
      copyTo: () => {
        if (selPaths.length)
          openDialog({ kind: 'moveCopy', op: 'copy', paths: selPaths, from: path })
      },
      deleteItems: () => void ops.remove(selPaths),
      deletePermanent: () => void ops.removePermanent(selPaths),
      batchRename: () => {
        if (selPaths.length) openDialog({ kind: 'batchRename', paths: selPaths, dir: path })
      },
      permissions: () => {
        if (selItems.length) openDialog({ kind: 'permissions', items: selItems })
      },
      symlinkTo: () => {
        if (focusItem)
          openDialog({ kind: 'new', mode: 'symlink', dir: path, target: focusItem.path })
      },
      copyPath: () => {
        if (!focusItem) return
        const p = focusItem.path
        void navigator.clipboard?.writeText(p).then(
          () => toast.success(t('fileops:copyPath.copied', { path: p })),
          () => toast.error(t('errors:UNKNOWN')),
        )
      },
      undo: () => void ops.undo(),
      openFolderProperties: () => {
        statItem(path)
          .then((stat) => setProperties(stat))
          .catch((e: unknown) => toast.error(e instanceof ApiError ? e.message : String(e)))
      },
    }),
    [ops, selPaths, selItems, path, tabId, focusItem, pasteTarget, openDialog, t],
  )
  const commitRename = useCallback(
    (item: Item, newName: string) => {
      setRenaming(null)
      void ops.rename(item.path, newName).then(() => focusGrid())
    },
    [ops],
  )
  const cancelRename = useCallback(() => {
    setRenaming(null)
    focusGrid()
  }, [])
  const siblingNames = useMemo(() => items.map((it) => it.name), [items])
  const renderRename = useCallback(
    (item: Item) => (
      <InlineRename
        item={item}
        siblings={siblingNames}
        onCommit={(n) => commitRename(item, n)}
        onCancel={cancelRename}
      />
    ),
    [siblingNames, commitRename, cancelRename],
  )
  // drop targets outside this screen (sidebar, breadcrumb) call the published handler
  useEffect(() => {
    setDropHandlers({
      items: (payload: DragPayload, targetDir, op) => {
        const paths = payload.paths.filter((p) => parentOf(p) !== targetDir)
        if (paths.length === 0) return
        void ops.transfer(op, paths, targetDir)
      },
      os: ops.osDrop,
    })
    return () => setDropHandlers(null)
  }, [ops])

  // --- shortcuts (browser scope) ---
  useEffect(() => pushScope('browser'), [])
  useEffect(() => {
    const offs = [
      registerShortcut('back', handlers.back),
      registerShortcut('forward', handlers.forward),
      registerShortcut('up', handlers.up),
      registerShortcut('refresh', handlers.refresh),
      registerShortcut('addressBar', handlers.editPath),
      registerShortcut('selectAll', () => setSelection(selectAll())),
      registerShortcut('invertSelection', () => setSelection(invertSelection(selection, items))),
      registerShortcut('escape', (e) => {
        if (e.target instanceof HTMLElement && e.target === filterRef.current) return
        if (selCount > 0) setSelection(clearSelection())
      }),
      registerShortcut('filter', () => filterRef.current?.focus()),
      registerShortcut('newFolder', () => openNew('folder')),
      registerShortcut('newFile', () => openNew('file')),
      registerShortcut('viewDetails', () => changeView('details')),
      registerShortcut('viewList', () => changeView('list')),
      registerShortcut('viewTiles', () => changeView('tiles')),
      registerShortcut('viewLarge', () => changeView('large')),
      registerShortcut('viewXLarge', () => changeView('xlarge')),
      registerShortcut('properties', () => {
        if (focusItem) setProperties(focusItem)
      }),
      registerShortcut('previewPanel', togglePreview),
      registerShortcut('newTab', () => openTab(path)),
      registerShortcut('closeTab', () => closeTab(tabId)),
      registerShortcut('nextTab', () => {
        const s = useTabsStore.getState()
        const i = s.tabs.findIndex((x) => x.id === s.activeId)
        activateTab(s.tabs[(i + 1) % s.tabs.length]!.id)
      }),
      registerShortcut('prevTab', () => {
        const s = useTabsStore.getState()
        const i = s.tabs.findIndex((x) => x.id === s.activeId)
        activateTab(s.tabs[(i - 1 + s.tabs.length) % s.tabs.length]!.id)
      }),
      registerShortcut('selectTab', (_e, keys) => {
        const n = Number(keys.replace('Alt+', ''))
        const target = useTabsStore.getState().tabs[n - 1]
        if (target) activateTab(target.id)
      }),
      registerShortcut('copy', fileOps.copy),
      registerShortcut('cut', fileOps.cut),
      registerShortcut('paste', fileOps.paste),
      registerShortcut('delete', fileOps.deleteItems),
      registerShortcut('deletePermanent', fileOps.deletePermanent),
      registerShortcut('rename', fileOps.rename),
      registerShortcut('undo', fileOps.undo),
    ]
    return () => offs.forEach((off) => off())
  }, [
    handlers,
    selection,
    items,
    selCount,
    setSelection,
    focusItem,
    togglePreview,
    openTab,
    closeTab,
    activateTab,
    path,
    tabId,
    setPref,
    changeView,
    openNew,
    fileOps,
  ])
  useDialogScope(!!quickLook || !!properties)

  // --- action registry context (palette, toolbar, context menu) ---
  const actionCtx = useMemo<ActionContext>(
    () => ({
      path,
      selection: selItems,
      selectionCount: selCount,
      total,
      clipboardCount: clipboard?.paths.length ?? 0,
      canUndo: canUndoNow(undoEntry),
      navigate: go,
      refresh: handlers.refresh,
      openNew,
      openTab,
      closeTab: () => closeTab(tabId),
      togglePreview,
      toggleTheme: () => {
        const ui = useUIStore.getState()
        ui.setTheme(ui.resolvedTheme === 'dark' ? 'light' : 'dark')
      },
      selectAll: () => setSelection(selectAll()),
      invertSelection: () => setSelection(invertSelection(selection, items)),
      clearSelection: () => setSelection(clearSelection()),
      openHelp: () => openHelp(true),
      openColumns: () => setColumnsOpen(true),
      openProperties: () => {
        if (focusItem) setProperties(focusItem)
      },
      quickLook: () => {
        if (focusItem) setQuickLook(focusItem)
      },
      focusFilter: () => filterRef.current?.focus(),
      open: () => {
        if (focusItem) onOpen(focusItem)
      },
      openInNewTab: () => {
        if (focusItem?.isDir) openTab(focusItem.path)
      },
      ...fileOps,
    }),
    [
      path,
      selItems,
      selCount,
      total,
      clipboard,
      undoEntry,
      go,
      handlers,
      openNew,
      openTab,
      closeTab,
      tabId,
      togglePreview,
      selection,
      items,
      setSelection,
      openHelp,
      focusItem,
      onOpen,
      fileOps,
    ],
  )
  useEffect(() => {
    setActionContext(actionCtx)
    return () => setActionContext(null)
  }, [actionCtx])

  // Decide the context-menu variant before Radix opens it: right-clicking an
  // unselected item selects it; otherwise the current selection is used.
  const onContextMenuCapture = useCallback(
    (e: React.MouseEvent) => {
      if (renaming) return
      const el =
        e.target instanceof HTMLElement ? e.target.closest<HTMLElement>('[data-path]') : null
      const p = el?.dataset.path
      if (p) {
        const item = items.find((it) => it.path === p)
        if (!item) return
        if (!isSelected(selection, p)) {
          setSelection(selectOnly(p))
          const idx = filtered.findIndex((it) => it.path === p)
          if (idx >= 0) setActiveIndex(idx)
          setMenuContext(item.isDir ? 'folder' : 'file')
          return
        }
        setMenuContext(menuContextFor({ selection: selItems, selectionCount: selCount }))
        return
      }
      // keyboard (Shift+F10 / Menu) on the grid: act on the selection
      const keyboard = e.button !== 2
      setMenuContext(
        keyboard && selCount > 0
          ? menuContextFor({ selection: selItems, selectionCount: selCount })
          : 'background',
      )
    },
    [renaming, items, filtered, selection, selItems, selCount, setSelection],
  )

  // Announce once per successful load of a folder.
  const announced = useRef<string | null>(null)
  useEffect(() => {
    if (isPending || isError) return
    const key = `${path}:${total}`
    if (announced.current === key) return
    announced.current = key
    announce(
      t('files:announce.opened', {
        name: path === ROOT ? t('files:nav.root') : baseName(path),
        count: total,
        n: formatNumber(total, i18n.resolvedLanguage ?? 'id'),
      }),
    )
  }, [path, total, isPending, isError, t, i18n.resolvedLanguage])
  const lastAnnouncedSel = useRef(0)
  useEffect(() => {
    if (selCount === lastAnnouncedSel.current) return
    lastAnnouncedSel.current = selCount
    if (selCount > 0)
      announce(
        t('files:announce.selected', {
          count: selCount,
          n: formatNumber(selCount, i18n.resolvedLanguage ?? 'id'),
        }),
      )
  }, [selCount, t, i18n.resolvedLanguage])

  const apiErr = error instanceof ApiError ? error : null
  const notFound = apiErr?.code === 'NOT_FOUND'
  const notDir = apiErr?.code === 'VALIDATION_FAILED' && apiErr.details.reason === 'not_a_directory'

  let body: React.ReactNode
  if (isPending) {
    // SPEC §9.7: the skeleton fades in only after 300 ms (CSS delay) so short loads do not flicker.
    body = (
      <div className="delayed-reveal space-y-2 p-3" aria-busy="true" data-testid="details-skeleton">
        {Array.from({ length: 12 }).map((_, i) => (
          <Skeleton key={i} className="h-7 w-full" />
        ))}
      </div>
    )
  } else if (isError) {
    body = (
      <EmptyState
        icon={FolderOpen}
        title={
          notFound
            ? t('files:error.notFound')
            : notDir
              ? t('files:error.notDirectory')
              : t('files:error.title')
        }
        description={apiErr ? t('files:error.code', { code: apiErr.code }) : (error?.message ?? '')}
        className="h-full"
        action={
          <>
            {!notFound && !notDir ? (
              <Button variant="outline" onClick={() => void refetch()}>
                {t('files:error.retry')}
              </Button>
            ) : null}
            {parent !== null ? (
              <Button variant="outline" onClick={() => go(parent)}>
                {t('files:error.goParent')}
              </Button>
            ) : null}
            <Button variant="primary" onClick={() => go(ROOT)}>
              {t('files:error.goHome')}
            </Button>
          </>
        }
      />
    )
  } else if (items.length === 0) {
    body = (
      <EmptyState
        icon={FolderOpen}
        title={t('files:empty.title')}
        description={t('files:empty.description')}
        className="h-full"
        action={
          <>
            <Button variant="outline" disabled>
              <Upload aria-hidden="true" />
              {t('files:empty.upload')}
            </Button>
            <Button variant="primary" onClick={() => openNew('folder')}>
              <FolderPlus aria-hidden="true" />
              {t('files:empty.newFolder')}
            </Button>
          </>
        }
      />
    )
  } else if (filtered.length === 0) {
    body = (
      <EmptyState
        icon={FolderOpen}
        title={t('files:filter.noMatch', { q: filter })}
        description={listing.hasNextPage ? t('files:filter.incomplete') : ''}
        className="h-full"
        action={
          <Button variant="outline" onClick={() => setFilter('')}>
            {t('files:filter.clear')}
          </Button>
        }
      />
    )
  } else {
    const common = {
      items: filtered,
      total: filter ? filtered.length : total,
      path,
      hasNextPage: listing.hasNextPage,
      isFetchingNextPage: listing.isFetchingNextPage,
      fetchNextPage: () => void listing.fetchNextPage(),
      onOpen,
      onQuickLook: setQuickLook,
      selection,
      onSelectionChange: setSelection,
      activeIndex,
      onActiveIndexChange: setActiveIndex,
      initialScroll: tab.scroll[path] ?? 0,
      onScrollChange: (off: number) => useTabsStore.getState().setScroll(path, off),
      renamingPath: renaming,
      renderRename,
      cutPaths: cut,
      onSlowClick: (item: Item) => setRenaming(item.path),
      dragPaths,
      dnd: !isMobile,
    }
    switch (view) {
      case 'list':
        body = <ListView {...common} />
        break
      case 'tiles':
        body = <TilesView {...common} />
        break
      case 'large':
        body = <LargeIconsView {...common} />
        break
      case 'xlarge':
        body = <XLIconsView {...common} />
        break
      default:
        body = (
          <DetailsView
            {...common}
            columns={isMobile ? MOBILE_PREF : columns}
            onColumnsChange={changeColumns}
            sort={sort}
            onSortChange={onHeaderSort}
            headerMenu={
              isMobile ? undefined : (
                <ColumnContextMenuItems columns={columns} onChange={changeColumns} />
              )
            }
            compact={isMobile}
          />
        )
    }
  }

  const visibleCount = orderedVisible(columns).length
  const viewMenu = (
    <ContextMenuRadioGroup value={view} onValueChange={(v) => changeView(v as ViewMode)}>
      {VIEW_MODES.map((m) => (
        <ContextMenuRadioItem key={m} value={m} data-testid={`cm-view-${m}`}>
          {t(`files:view.${m}`)}
        </ContextMenuRadioItem>
      ))}
    </ContextMenuRadioGroup>
  )
  const sortMenu = (
    <>
      <ContextMenuRadioGroup
        value={sort.field}
        onValueChange={(f) => changeSort({ field: f as SortField, dir: sort.dir })}
      >
        {SORT_FIELDS.map((f) => (
          <ContextMenuRadioItem key={f} value={f} data-testid={`cm-sort-${f}`}>
            {t(`files:sort.field.${f}`)}
          </ContextMenuRadioItem>
        ))}
      </ContextMenuRadioGroup>
      <ContextMenuSeparator />
      <ContextMenuRadioGroup
        value={sort.dir}
        onValueChange={(d) => changeSort({ ...sort, dir: d as SortPref['dir'] })}
      >
        <ContextMenuRadioItem value="asc">{t('files:sort.asc')}</ContextMenuRadioItem>
        <ContextMenuRadioItem value="desc">{t('files:sort.desc')}</ContextMenuRadioItem>
      </ContextMenuRadioGroup>
      <ContextMenuSeparator />
      <ContextMenuCheckboxItem
        checked={foldersFirst}
        onCheckedChange={(v) => setPref('view.folders_first', v === true)}
      >
        {t('files:sort.foldersFirst')}
      </ContextMenuCheckboxItem>
    </>
  )
  return (
    <div className="relative flex h-full min-h-0 flex-col" data-testid="browser" data-view={view}>
      <AddressBar
        path={path}
        canBack={tabs.canBack()}
        canForward={tabs.canForward()}
        canUp={parent !== null}
        onBack={handlers.back}
        onForward={handlers.forward}
        onUp={handlers.up}
        onRefresh={handlers.refresh}
        onNavigate={go}
        editing={editing}
        onEditingChange={setEditing}
        compact={isMobile}
        refreshing={isFetching && !isPending}
      >
        {!isMobile ? (
          <QuickFilter
            ref={filterRef}
            value={filter}
            onChange={setFilter}
            incomplete={listing.hasNextPage}
            onServerSearch={() => toast(t('files:filter.serverSearchSoon'))}
          />
        ) : null}
      </AddressBar>
      <TabBar
        tabs={tabs.tabs}
        activeId={tabs.activeId}
        onActivate={activateTab}
        onClose={closeTab}
        onCloseOthers={(id) => useTabsStore.getState().closeOthers(id)}
        onCloseRight={(id) => useTabsStore.getState().closeRight(id)}
        onNew={() => openTab(path)}
        onReorder={(id, to) => useTabsStore.getState().reorder(id, to)}
      />
      <Toolbar
        onNew={openNew}
        templates={templates.data ?? []}
        actions={<ToolbarActions ctx={actionCtx} compact={isMobile} />}
        compact={isMobile}
        previewOpen={previewOpen}
        onTogglePreview={togglePreview}
        viewSwitcher={
          <ViewSwitcher
            mode={view}
            onModeChange={changeView}
            rememberPerFolder={rememberPerFolder}
            onRememberPerFolderChange={(v) => setPref('view.remember_per_folder', v)}
          />
        }
        columnsMenu={
          <ColumnMenu
            columns={columns}
            onChange={changeColumns}
            open={columnsOpen}
            onOpenChange={setColumnsOpen}
          />
        }
        sortMenu={
          <SortMenu
            sort={sort}
            onSortChange={changeSort}
            foldersFirst={foldersFirst}
            onFoldersFirstChange={(v) => setPref('view.folders_first', v)}
          />
        }
      />
      <div className="relative flex min-h-0 flex-1">
        <BrowserContextMenu
          ctx={menuOpen || !dialogOpen ? actionCtx : null}
          context={menuContext}
          viewMenu={viewMenu}
          sortMenu={sortMenu}
          columnsMenu={<ColumnContextMenuItems columns={columns} onChange={changeColumns} />}
          templates={templates.data ?? []}
          onOpenChange={setMenuOpen}
        >
          <div
            className="flex min-h-0 min-w-0 flex-1 flex-col"
            data-columns={visibleCount}
            onContextMenuCapture={onContextMenuCapture}
          >
            {body}
          </div>
        </BrowserContextMenu>
        {previewOpen && !isMobile ? (
          <PreviewPanel
            items={selItems}
            count={selCount}
            onClose={togglePreview}
            overlay={previewOverlay}
          />
        ) : null}
      </div>
      <StatusBar
        total={filter ? filtered.length : total}
        selectedCount={selCount}
        selectedBytes={selBytes}
      />
      <FileOpsDialogs ops={ops} />
      <QuickLookDialog item={quickLook} onClose={() => setQuickLook(null)} />
      <PropertiesDialog item={properties} onClose={() => setProperties(null)} />
    </div>
  )
}
