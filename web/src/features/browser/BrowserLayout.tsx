import { useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ArchiveBrowser } from '@/features/archive/ArchiveBrowser'
import { QuickLook } from '@/features/viewer/QuickLook'
import { usePlayerStore } from '@/stores/player'
import { setMiniPlayerSlot } from '@/features/viewer/audio/miniPlayerSlot'
import { ViewerOverlay } from '@/features/viewer/ViewerOverlay'
import { opensInViewer } from '@/features/viewer/viewers'
import { EditorModal } from '@/features/editor/EditorModal'
import { isEditableByName } from '@/features/editor/languages'
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
import {
  setDropHandlers,
  dropTargetProps,
  setFavoriteDropHandler,
  type DragPayload,
  parentOf,
} from '@/features/file-ops/dnd'
import { isTagColor, recordOpen, type TagColor } from '@/features/meta/api'
import { useMeta } from '@/features/meta/useMeta'
import { NoteDialog } from '@/features/meta/NoteDialog'
import { FileOpsDialogs } from '@/features/file-ops/FileOpsDialogs'
import { canUndoNow, focusGrid, useFileOpsDialogs, useUndoStore } from '@/features/file-ops/store'
import { useFileOps } from '@/features/file-ops/useFileOps'
import { useDownload } from '@/features/download/useDownload'
import { DropZone } from '@/features/upload/DropZone'
import { installPasteUpload } from '@/features/upload/paste'
import { UploadInputs, type UploadInputsHandle } from '@/features/upload/UploadInputs'
import { uploadFromClipboard, uploadFromFileList } from '@/features/upload/useUploader'
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
import { useSessionStore } from '@/stores/session'
import { useUIStore } from '@/stores/ui'
import { AddressBar } from './AddressBar'
import {
  getFolderPref,
  isViewMode,
  putFolderPref,
  statItem,
  VIEW_MODES,
  type Item,
  type ListResponse,
  type SortField,
  type ViewMode,
} from './api'
import { ColumnContextMenuItems, ColumnMenu } from './ColumnMenu'
import { MOBILE_COLUMNS, normalizeColumns, orderedVisible, type ColumnsPref } from './columns'
import { BrowserContextMenu } from './ContextMenu'
import { DetailsView } from './DetailsView'
import { InlineRename } from './InlineRename'
import { PropertiesDialog, type PropertiesTarget } from '@/features/properties/PropertiesDialog'
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
import { TagFilter } from './TagFilter'
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

export function BrowserLayout({
  path,
  selectName,
  archivePath,
  viewPath,
}: {
  path: string
  selectName?: string | undefined
  /** `?archive=<path>`: the archive browser overlay (Step 14b) is open on this archive. */
  archivePath?: string | undefined
  /** `?view=<path>`: the viewer overlay (Step 17a) is open on this file. */
  viewPath?: string | undefined
}) {
  const { t, i18n } = useTranslation(['files', 'fileops', 'errors', 'common', 'meta'])
  const navigate = useNavigate()
  const qc = useQueryClient()
  const isMobile = useIsMobile()
  const previewOverlay = useMediaQuery('(max-width: 1279px)')
  const [editing, setEditing] = useState(false)
  // Quick Look (Space): compact viewers in a light modal (SPEC §8.14, Step 17b)
  const [quickLook, setQuickLook] = useState<Item | null>(null)
  const [editorPath, setEditorPath] = useState<string | null>(null)
  const [properties, setProperties] = useState<PropertiesTarget | null>(null)
  const [columnsOpen, setColumnsOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [renaming, setRenaming] = useState<string | null>(null)
  const [menuContext, setMenuContext] = useState<MenuContext>('background')
  const [menuOpen, setMenuOpen] = useState(false)
  const [tagFilter, setTagFilter] = useState<TagColor | null>(null)
  const [noteTarget, setNoteTarget] = useState<Item | null>(null)
  const meta = useMeta()
  const filterRef = useRef<HTMLInputElement>(null)
  const openHelp = useOverlaysStore((s) => s.setHelp)
  const ops = useFileOps()
  const dl = useDownload()
  // uploads (Step 12a): hidden inputs + the folder the next pick targets
  const uploadInputs = useRef<UploadInputsHandle>(null)
  const uploadDest = useRef<string>(path)
  const contentRef = useRef<HTMLDivElement>(null)
  const openDialog = useFileOpsDialogs((s) => s.open)
  // SPEC §8.10: the menu entry and toolbar button disappear when the admin
  // turned remote downloads off (the API answers 403 anyway)
  const remoteDownloadEnabled = useSessionStore((s) => s.config?.remoteDownloadEnabled ?? true)
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
    tag: tagFilter ?? undefined,
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
    setTagFilter(null)
    setRenaming(null)
  }
  useEffect(() => {
    useSelectionStore.getState().clear(tabId)
  }, [path, tabId])
  // "Buka lokasi file": select + reveal the item named by ?select= once the
  // folder is loaded (render-time state adjustment, then one effect for the
  // store write and the URL clean-up).
  const [revealKey, setRevealKey] = useState<string | null>(null)
  const revealIdx = selectName && !isPending ? items.findIndex((it) => it.name === selectName) : -1
  const revealReady = !!selectName && !isPending && (revealIdx >= 0 || !listing.hasNextPage)
  const nextRevealKey = revealReady ? `${path}/${selectName}` : null
  if (nextRevealKey && revealKey !== nextRevealKey) {
    setRevealKey(nextRevealKey)
    if (revealIdx >= 0) setActiveIndex(revealIdx)
  }
  useEffect(() => {
    if (!revealKey || !selectName || revealKey !== `${path}/${selectName}`) return
    const target = items.find((it) => it.name === selectName)
    if (target) {
      useSelectionStore.getState().set(tabId, selectOnly(target.path))
      announce(t('files:announce.selected', { count: 1, n: '1' }))
    }
    void navigate({
      to: '/files/$',
      params: { _splat: pathToSplat(path) },
      search: {},
      replace: true,
    })
  }, [revealKey, selectName, items, path, tabId, navigate, t])
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

  const openArchive = useCallback(
    (archive: string) =>
      void navigate({
        to: '/files/$',
        params: { _splat: pathToSplat(path) },
        search: { archive },
      }),
    [navigate, path],
  )
  const closeArchive = useCallback(
    () => void navigate({ to: '/files/$', params: { _splat: pathToSplat(path) }, search: {} }),
    [navigate, path],
  )
  // --- viewer overlay (Step 17a): `?view=<path>` is a history entry so Back closes it ---
  const viewerPushed = useRef(false)
  const openViewer = useCallback(
    (file: string) => {
      viewerPushed.current = true
      void navigate({
        to: '/files/$',
        params: { _splat: pathToSplat(path) },
        search: { view: file },
      })
    },
    [navigate, path],
  )
  const viewerNavigate = useCallback(
    (item: Item) =>
      void navigate({
        to: '/files/$',
        params: { _splat: pathToSplat(path) },
        search: { view: item.path },
        replace: true,
      }),
    [navigate, path],
  )
  const closeViewer = useCallback(() => {
    if (viewerPushed.current) {
      viewerPushed.current = false
      window.history.back()
      return
    }
    // deep link: nothing to go back to inside the app
    void navigate({ to: '/files/$', params: { _splat: pathToSplat(path) }, search: {} })
  }, [navigate, path])
  const viewerOpenLocation = useCallback(
    (item: Item) => {
      viewerPushed.current = false
      void navigate({
        to: '/files/$',
        params: { _splat: pathToSplat(parentPath(item.path) ?? ROOT) },
        search: { select: item.name },
      })
    },
    [navigate],
  )
  const viewerEdit = useCallback(
    (item: Item) => {
      closeViewer()
      setEditorPath(item.path)
    },
    [closeViewer],
  )
  const onOpen = useCallback(
    (item: Item) => {
      if (item.isDir || (item.isSymlink && !item.broken && item.type === 'dir')) {
        go(item.path)
        return
      }
      if (item.category === 'archive') {
        // SPEC §8.11: "Buka" on an archive opens the archive browser
        openArchive(item.path)
        recordOpen(item.path).catch(() => {})
        return
      }
      if (opensInViewer(item)) {
        // SPEC §8.14: every kind with a viewer opens in the overlay (it records
        // the open); text/code is read-only there and "Edit" opens the editor
        openViewer(item.path)
        return
      }
      if (isEditableByName(item.name, item.mime, item.category)) {
        // text formats without a viewer kind (e.g. unknown extensions with a
        // text MIME) still go to the editor
        setEditorPath(item.path)
        recordOpen(item.path).catch(() => {})
        return
      }
      // Office documents and unknown types: the overlay shows the info card
      openViewer(item.path)
    },
    [go, openArchive, openViewer],
  )
  const onQuickLook = useCallback((item: Item) => setQuickLook(item), [])
  const quickLookMove = useCallback(
    (item: Item) => {
      useSelectionStore.getState().set(tabId, selectOnly(item.path))
      const i = filtered.findIndex((it) => it.path === item.path)
      if (i >= 0) setActiveIndex(i)
    },
    [tabId, filtered],
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
      folderSize: () => {
        if (focusItem?.isDir) void ops.dirSize(focusItem.path)
      },
      checksum: () => {
        if (focusItem && !focusItem.isDir) setProperties({ items: [focusItem], tab: 'checksum' })
      },
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
      download: () => {
        const target = selItems.length ? selItems : focusItem ? [focusItem] : []
        if (target.length) void dl.download(target)
      },
      downloadZip: () => {
        const target = selItems.length ? selItems : focusItem ? [focusItem] : []
        if (target.length) void dl.downloadZip(target)
      },
      uploadFile: () => {
        uploadDest.current = path
        uploadInputs.current?.pickFiles()
      },
      uploadFolder: () => {
        uploadDest.current = path
        uploadInputs.current?.pickFolder()
      },
      uploadHere: () => {
        if (!focusItem?.isDir) return
        uploadDest.current = focusItem.path
        uploadInputs.current?.pickFiles()
      },
      remoteDownload: () => openDialog({ kind: 'remoteDownload', dir: path }),
      remoteDownloadEnabled,
      searchHere: () => {
        const dir = focusItem?.isDir ? focusItem.path : path
        void navigate({ to: '/search', search: { path: dir !== '/' ? dir : undefined } })
      },
      contentSearch: () =>
        void navigate({
          to: '/search',
          search: { kind: 'content', path: path !== '/' ? path : undefined },
        }),
      compress: () => {
        const target = selPaths.length ? selPaths : focusItem ? [focusItem.path] : []
        if (target.length) openDialog({ kind: 'compress', paths: target, dir: path })
      },
      extractArchive: () => {
        const item = selItems.length === 1 ? selItems[0] : focusItem
        if (item && !item.isDir && item.category === 'archive')
          openDialog({ kind: 'extract', archive: item, dir: path })
      },
      browseArchive: () => {
        const item = selItems.length === 1 ? selItems[0] : focusItem
        if (item && !item.isDir && item.category === 'archive') openArchive(item.path)
      },
      addToPlaylist: () => {
        const tracks = (selItems.length ? selItems : focusItem ? [focusItem] : [])
          .filter((it) => !it.isDir && it.category === 'audio')
          .map((it) => ({
            path: it.path,
            name: it.name,
            mime: it.mime,
            ext: it.ext,
            size: it.size,
          }))
        const added = usePlayerStore.getState().add(tracks)
        toast.success(t('files:playlist.added', { count: added }))
      },
      openFolderProperties: () => {
        statItem(path)
          .then((stat) => setProperties({ items: [stat] }))
          .catch((e: unknown) => toast.error(e instanceof ApiError ? e.message : String(e)))
      },
    }),
    [
      ops,
      dl,
      navigate,
      selPaths,
      selItems,
      path,
      tabId,
      focusItem,
      pasteTarget,
      openDialog,
      t,
      remoteDownloadEnabled,
      openArchive,
    ],
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
  // Ctrl+V with OS files/images on the clipboard uploads into the open folder (SPEC §8.8)
  useEffect(
    () =>
      installPasteUpload(
        () => path,
        (dt, dest) => void uploadFromClipboard(dt, dest),
      ),
    [path],
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
      registerShortcut('contentSearch', () =>
        navigate({
          to: '/search',
          search: { kind: 'content', path: path !== '/' ? path : undefined },
        }),
      ),
      registerShortcut('newFolder', () => openNew('folder')),
      registerShortcut('newFile', () => openNew('file')),
      registerShortcut('viewDetails', () => changeView('details')),
      registerShortcut('viewList', () => changeView('list')),
      registerShortcut('viewTiles', () => changeView('tiles')),
      registerShortcut('viewLarge', () => changeView('large')),
      registerShortcut('viewXLarge', () => changeView('xlarge')),
      registerShortcut('properties', () => {
        if (selItems.length > 1) setProperties({ items: selItems })
        else if (focusItem) setProperties({ items: [focusItem] })
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
      registerShortcut('upload', fileOps.uploadFile),
    ]
    return () => offs.forEach((off) => off())
  }, [
    handlers,
    navigate,
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
    selItems,
  ])
  useDialogScope(!!properties || !!archivePath || !!editorPath)
  // the viewer and Quick Look push their own modal scope (`viewer`)

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
      toggleFavorite: () =>
        void meta.toggleFavorite(selItems.length ? selItems : focusItem ? [focusItem] : []),
      togglePin: () => {
        if (focusItem) void meta.togglePin([focusItem])
      },
      editNote: () => {
        if (focusItem) setNoteTarget(focusItem)
      },
      setTag: (color) => {
        const paths = selPaths.length ? selPaths : focusItem ? [focusItem.path] : []
        void meta.setTag(paths, color && isTagColor(color) ? color : null)
      },
      openInNewWindow: () => {
        if (focusItem?.isDir)
          window.open(
            `/files${pathToSplat(focusItem.path) ? `/${pathToSplat(focusItem.path)}` : ''}`,
            '_blank',
            'noopener',
          )
      },
      selectAll: () => setSelection(selectAll()),
      invertSelection: () => setSelection(invertSelection(selection, items)),
      clearSelection: () => setSelection(clearSelection()),
      openHelp: () => openHelp(true),
      openColumns: () => setColumnsOpen(true),
      openProperties: () => {
        if (selItems.length > 1) setProperties({ items: selItems })
        else if (focusItem) setProperties({ items: [focusItem] })
      },
      quickLook: () => {
        if (focusItem) onQuickLook(focusItem)
      },
      focusFilter: () => filterRef.current?.focus(),
      open: () => {
        if (focusItem) onOpen(focusItem)
      },
      openInEditor: () => {
        if (focusItem && !focusItem.isDir) setEditorPath(focusItem.path)
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
      onQuickLook,
      fileOps,
      meta,
      selPaths,
    ],
  )
  useEffect(() => {
    setActionContext(actionCtx)
    return () => setActionContext(null)
  }, [actionCtx])
  // sidebar "Favorit" is a drop target for items
  useEffect(() => {
    setFavoriteDropHandler((paths) => void meta.addFavorites(paths))
    return () => setFavoriteDropHandler(null)
  }, [meta])
  const saveNote = useCallback((p: string, note: string) => meta.setNote(p, note), [meta])

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
  } else if (items.length === 0 && !tagFilter) {
    body = (
      <EmptyState
        icon={FolderOpen}
        title={t('files:empty.title')}
        description={t('files:empty.description')}
        className="h-full"
        action={
          <>
            <Button
              variant="outline"
              onClick={() => fileOps.uploadFile()}
              data-testid="empty-upload"
            >
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
  } else if (filtered.length === 0 && (filter || tagFilter)) {
    body = (
      <EmptyState
        icon={FolderOpen}
        title={t('files:filter.noMatch', {
          q: filter || t(`meta:tag.colors.${tagFilter ?? 'red'}`),
        })}
        description={listing.hasNextPage ? t('files:filter.incomplete') : ''}
        className="h-full"
        action={
          <Button
            variant="outline"
            onClick={() => {
              setFilter('')
              setTagFilter(null)
            }}
          >
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
      onQuickLook,
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
      onNoteSave: saveNote,
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
          <>
            <QuickFilter
              ref={filterRef}
              value={filter}
              onChange={setFilter}
              incomplete={listing.hasNextPage}
              onServerSearch={() => toast(t('files:filter.serverSearchSoon'))}
            />
            <TagFilter value={tagFilter} onChange={setTagFilter} />
          </>
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
        onUpload={(kind) => (kind === 'folder' ? fileOps.uploadFolder() : fileOps.uploadFile())}
        onRemote={remoteDownloadEnabled ? fileOps.remoteDownload : undefined}
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
      <div className="relative flex min-h-0 flex-1" ref={contentRef}>
        <DropZone container={contentRef} path={path} />
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
            // OS drops on an empty folder (no grid underneath) still upload here
            {...(!isMobile ? dropTargetProps({ dir: path }) : {})}
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
            onDownload={selItems.length ? () => void dl.download(selItems) : undefined}
            onOpen={onOpen}
          />
        ) : null}
      </div>
      {/* the persistent mini-player (AppShell) renders here, above the status bar (SPEC §8.14) */}
      <div
        ref={setMiniPlayerSlot}
        className="shrink-0 empty:hidden"
        data-testid="mini-player-slot"
      />
      <StatusBar
        total={filter ? filtered.length : total}
        selectedCount={selCount}
        selectedBytes={selBytes}
      />
      <FileOpsDialogs ops={ops} />
      <UploadInputs
        ref={uploadInputs}
        onFiles={(files) => void uploadFromFileList(files, uploadDest.current)}
      />
      <NoteDialog item={noteTarget} onClose={() => setNoteTarget(null)} onSave={saveNote} />
      {archivePath ? <ArchiveBrowser archivePath={archivePath} onClose={closeArchive} /> : null}
      {viewPath ? (
        <ViewerOverlay
          path={viewPath}
          items={filtered}
          paging={{
            hasNextPage: listing.hasNextPage,
            isFetchingNextPage: listing.isFetchingNextPage,
            fetchNextPage: () => void listing.fetchNextPage(),
          }}
          onClose={closeViewer}
          onNavigate={viewerNavigate}
          onEdit={viewerEdit}
          onOpenLocation={viewerOpenLocation}
          onOpenPath={openViewer}
          onBrowseArchive={(it) => openArchive(it.path)}
          restoreFocus={focusGrid}
        />
      ) : null}
      <EditorModal path={editorPath} onClose={() => setEditorPath(null)} />
      {quickLook ? (
        <QuickLook
          key={quickLook.path}
          item={quickLook}
          items={filtered}
          onClose={() => setQuickLook(null)}
          onMove={quickLookMove}
          onNoteSave={saveNote}
          onToggleFavorite={(it) => void meta.toggleFavorite([it])}
          onTogglePin={(it) => void meta.togglePin([it])}
          onSetTag={(it, c) => void meta.setTag([it.path], c)}
          restoreFocus={focusGrid}
        />
      ) : null}
      <PropertiesDialog
        target={properties}
        onClose={() => setProperties(null)}
        meta={meta}
        onDownload={(its) => void dl.download(its)}
        onDirSize={(p, res) => {
          qc.setQueriesData<InfiniteData<ListResponse, string | null>>(
            { queryKey: ['list', parentOf(p)] },
            (old) =>
              old && Array.isArray(old.pages)
                ? {
                    ...old,
                    pages: old.pages.map((pg) => ({
                      ...pg,
                      items: pg.items.map((it) => (it.path === p ? { ...it, size: res.size } : it)),
                    })),
                  }
                : old,
          )
        }}
      />
    </div>
  )
}
