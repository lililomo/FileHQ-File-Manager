import type { LucideIcon } from 'lucide-react'
import {
  Archive,
  ArchiveRestore,
  ArrowDownUp,
  ClipboardPaste,
  Columns3,
  Copy,
  CopyMinus,
  CopyPlus,
  Download,
  ExternalLink,
  AppWindow,
  PinOff,
  StarOff,
  FilePlus2,
  FileSearch,
  FolderInput,
  FolderOutput,
  FolderOpen,
  FolderPlus,
  FolderSearch,
  FolderSymlink,
  Globe,
  Hash,
  History,
  Info,
  Keyboard,
  LayoutGrid,
  LayoutList,
  Link2,
  Link as LinkIcon,
  Lock,
  MapPin,
  PackageOpen,
  PanelRight,
  Pencil,
  Pin,
  Plus,
  Replace,
  RotateCw,
  Ruler,
  Scissors,
  Search,
  Share2,
  SquareDashedMousePointer,
  SquareMousePointer,
  Star,
  StickyNote,
  SunMoon,
  Tag,
  Trash2,
  Undo2,
  Upload,
  X,
  FolderUp,
} from 'lucide-react'
import type { Item } from '@/features/browser/api'

/** Single source of truth for user actions (PROMPT 7b task 9 / 9b task 1): the
 *  command palette, toolbar and context menu all read from this registry.
 *  Labels are i18n keys under `files:actions.*`; `shortcut` references
 *  `lib/shortcuts.ts` ids so tooltips and the "?" dialog stay in sync.
 *  Actions whose backend lands in a later step are registered with
 *  `available: false` and are hidden everywhere until that step flips them. */

export type ActionGroup =
  | 'navigate'
  | 'create'
  | 'view'
  | 'select'
  | 'app'
  | 'fileops'
  | 'transfer'
  | 'meta'
  | 'archive'
  | 'share'

export interface ActionContext {
  path: string
  selection: Item[]
  selectionCount: number
  total: number
  clipboardCount: number
  canUndo: boolean
  navigate: (path: string) => void
  refresh: () => void
  openNew: (kind: 'folder' | 'file' | 'template' | 'symlink', opts?: { template?: string }) => void
  openTab: (path: string) => void
  closeTab: () => void
  togglePreview: () => void
  toggleTheme: () => void
  selectAll: () => void
  invertSelection: () => void
  clearSelection: () => void
  openHelp: () => void
  openColumns: () => void
  openProperties: () => void
  openFolderProperties: () => void
  quickLook: () => void
  focusFilter: () => void
  open: () => void
  openInNewTab: () => void
  copy: () => void
  cut: () => void
  paste: () => void
  duplicate: () => void
  rename: () => void
  moveTo: () => void
  copyTo: () => void
  deleteItems: () => void
  deletePermanent: () => void
  batchRename: () => void
  permissions: () => void
  symlinkTo: () => void
  copyPath: () => void
  undo: () => void
  /** Step 10a: folder size (Ukuran column + Properties) and checksum tab. */
  folderSize?: () => void
  checksum?: () => void
  /** Recycle Bin page only (context "trash"). */
  restore?: () => void
  restoreTo?: () => void
  purge?: () => void
  /** Step 10b: organisation metadata (SPEC §8.7). */
  toggleFavorite?: () => void
  togglePin?: () => void
  editNote?: () => void
  setTag?: (color: string | null) => void
  /** Only provided by Search / Recent lists: navigate to the folder and select the item. */
  openLocation?: () => void
  openInNewWindow?: () => void
  /** Step 12a: upload into the open folder / the focused folder. */
  uploadFile?: () => void
  uploadFolder?: () => void
  uploadHere?: () => void
}

export interface ActionDef {
  id: string
  labelKey: string
  group: ActionGroup
  icon: LucideIcon
  shortcut?: string
  /** False until the owning step ships the backend: hidden everywhere. */
  available?: boolean
  /** Rendered in the danger colour, always last in its menu (SPEC §9.5). */
  destructive?: boolean
  /** Rendered as a submenu by the context menu (children are built there). */
  submenu?: boolean
  /** Hidden when false (SPEC §9.5: inapplicable actions are hidden, not disabled). */
  isApplicable: (ctx: ActionContext) => boolean
  run: (ctx: ActionContext) => void
}

const always = () => true
const noop = () => {}
const one = (c: ActionContext) => c.selectionCount === 1 && c.selection.length === 1
const oneFile = (c: ActionContext) => one(c) && !c.selection[0]!.isDir
const oneFolder = (c: ActionContext) => one(c) && c.selection[0]!.isDir
const some = (c: ActionContext) => c.selectionCount > 0
const many = (c: ActionContext) => c.selectionCount > 1

export const ACTIONS: ActionDef[] = [
  // --- open ---
  {
    id: 'open',
    labelKey: 'open',
    group: 'navigate',
    icon: FolderOpen,
    shortcut: 'open',
    isApplicable: one,
    run: (c) => c.open(),
  },
  {
    id: 'openWith',
    labelKey: 'openWith',
    group: 'navigate',
    icon: ExternalLink,
    available: false,
    submenu: true,
    isApplicable: oneFile,
    run: noop,
  },
  {
    id: 'openInNewTab',
    labelKey: 'openInNewTab',
    group: 'navigate',
    icon: Plus,
    isApplicable: oneFolder,
    run: (c) => c.openInNewTab(),
  },
  {
    id: 'openInNewWindow',
    labelKey: 'openInNewWindow',
    group: 'navigate',
    icon: AppWindow,
    isApplicable: (c) => oneFolder(c) && !!c.openInNewWindow,
    run: (c) => c.openInNewWindow?.(),
  },
  {
    id: 'quickLook',
    labelKey: 'quickLook',
    group: 'select',
    icon: Search,
    shortcut: 'quickLook',
    isApplicable: oneFile,
    run: (c) => c.quickLook(),
  },
  // --- transfer / share (later steps) ---
  {
    id: 'download',
    labelKey: 'download',
    group: 'transfer',
    icon: Download,
    available: false,
    isApplicable: oneFile,
    run: noop,
  },
  {
    id: 'downloadZip',
    labelKey: 'downloadZip',
    group: 'transfer',
    icon: Archive,
    available: false,
    isApplicable: (c) => oneFolder(c) || many(c),
    run: noop,
  },
  {
    id: 'share',
    labelKey: 'share',
    group: 'share',
    icon: Share2,
    available: false,
    isApplicable: one,
    run: noop,
  },
  {
    id: 'copyShareLink',
    labelKey: 'copyShareLink',
    group: 'share',
    icon: Link2,
    available: false,
    isApplicable: (c) => one(c) && c.selection[0]!.shared !== 'none',
    run: noop,
  },
  // --- metadata (10b) ---
  {
    id: 'favorite',
    labelKey: 'favorite',
    group: 'meta',
    icon: Star,
    isApplicable: (c) => some(c) && !!c.toggleFavorite && !c.selection.every((it) => it.favorite),
    run: (c) => c.toggleFavorite?.(),
  },
  {
    id: 'unfavorite',
    labelKey: 'unfavorite',
    group: 'meta',
    icon: StarOff,
    isApplicable: (c) =>
      some(c) &&
      !!c.toggleFavorite &&
      c.selection.length > 0 &&
      c.selection.every((it) => it.favorite),
    run: (c) => c.toggleFavorite?.(),
  },
  {
    id: 'pin',
    labelKey: 'pin',
    group: 'meta',
    icon: Pin,
    isApplicable: (c) => one(c) && !!c.togglePin && !c.selection[0]!.pinned,
    run: (c) => c.togglePin?.(),
  },
  {
    id: 'unpin',
    labelKey: 'unpin',
    group: 'meta',
    icon: PinOff,
    isApplicable: (c) => one(c) && !!c.togglePin && c.selection[0]!.pinned,
    run: (c) => c.togglePin?.(),
  },
  {
    id: 'note',
    labelKey: 'note',
    group: 'meta',
    icon: StickyNote,
    isApplicable: (c) => one(c) && !!c.editNote,
    run: (c) => c.editNote?.(),
  },
  {
    id: 'tagColor',
    labelKey: 'tagColor',
    group: 'meta',
    icon: Tag,
    submenu: true,
    isApplicable: (c) => some(c) && !!c.setTag,
    run: noop,
  },
  // --- file operations (9b) ---
  {
    id: 'copy',
    labelKey: 'copy',
    group: 'fileops',
    icon: Copy,
    shortcut: 'copy',
    isApplicable: some,
    run: (c) => c.copy(),
  },
  {
    id: 'cut',
    labelKey: 'cut',
    group: 'fileops',
    icon: Scissors,
    shortcut: 'cut',
    isApplicable: some,
    run: (c) => c.cut(),
  },
  {
    id: 'paste',
    labelKey: 'paste',
    group: 'fileops',
    icon: ClipboardPaste,
    shortcut: 'paste',
    isApplicable: (c) => c.clipboardCount > 0,
    run: (c) => c.paste(),
  },
  {
    id: 'duplicate',
    labelKey: 'duplicate',
    group: 'fileops',
    icon: CopyPlus,
    isApplicable: one,
    run: (c) => c.duplicate(),
  },
  {
    id: 'rename',
    labelKey: 'rename',
    group: 'fileops',
    icon: Pencil,
    shortcut: 'rename',
    isApplicable: one,
    run: (c) => c.rename(),
  },
  {
    id: 'moveTo',
    labelKey: 'moveTo',
    group: 'fileops',
    icon: FolderInput,
    isApplicable: some,
    run: (c) => c.moveTo(),
  },
  {
    id: 'copyTo',
    labelKey: 'copyTo',
    group: 'fileops',
    icon: FolderSymlink,
    isApplicable: some,
    run: (c) => c.copyTo(),
  },
  {
    id: 'batchRename',
    labelKey: 'batchRename',
    group: 'fileops',
    icon: Replace,
    isApplicable: many,
    run: (c) => c.batchRename(),
  },
  {
    id: 'undo',
    labelKey: 'undo',
    group: 'fileops',
    icon: Undo2,
    shortcut: 'undo',
    isApplicable: (c) => c.canUndo,
    run: (c) => c.undo(),
  },
  // --- archive (14) ---
  {
    id: 'compress',
    labelKey: 'compress',
    group: 'archive',
    icon: Archive,
    available: false,
    isApplicable: some,
    run: noop,
  },
  {
    id: 'extract',
    labelKey: 'extract',
    group: 'archive',
    icon: PackageOpen,
    available: false,
    submenu: true,
    isApplicable: (c) => oneFile(c) && c.selection[0]!.category === 'archive',
    run: noop,
  },
  {
    id: 'browseArchive',
    labelKey: 'browseArchive',
    group: 'archive',
    icon: FolderSearch,
    available: false,
    isApplicable: (c) => oneFile(c) && c.selection[0]!.category === 'archive',
    run: noop,
  },
  {
    id: 'folderSize',
    labelKey: 'folderSize',
    group: 'meta',
    icon: Ruler,
    isApplicable: oneFolder,
    run: (c) => c.folderSize?.(),
  },
  {
    id: 'uploadHere',
    labelKey: 'uploadHere',
    group: 'transfer',
    icon: Upload,
    isApplicable: oneFolder,
    run: (c) => c.uploadHere?.(),
  },
  {
    id: 'searchHere',
    labelKey: 'searchHere',
    group: 'navigate',
    icon: FileSearch,
    available: false,
    isApplicable: oneFolder,
    run: noop,
  },
  // --- attributes ---
  {
    id: 'permissions',
    labelKey: 'permissions',
    group: 'fileops',
    icon: Lock,
    isApplicable: some,
    run: (c) => c.permissions(),
  },
  {
    id: 'symlink',
    labelKey: 'symlink',
    group: 'fileops',
    icon: LinkIcon,
    isApplicable: one,
    run: (c) => c.symlinkTo(),
  },
  {
    id: 'checksum',
    labelKey: 'checksum',
    group: 'meta',
    icon: Hash,
    isApplicable: oneFile,
    run: (c) => c.checksum?.(),
  },
  {
    id: 'versions',
    labelKey: 'versions',
    group: 'meta',
    icon: History,
    available: false,
    isApplicable: oneFile,
    run: noop,
  },
  {
    id: 'copyPath',
    labelKey: 'copyPath',
    group: 'fileops',
    icon: MapPin,
    isApplicable: one,
    run: (c) => c.copyPath(),
  },
  {
    id: 'openLocation',
    labelKey: 'openLocation',
    group: 'navigate',
    icon: FolderOpen,
    isApplicable: (c) => one(c) && !!c.openLocation,
    run: (c) => c.openLocation?.(),
  },
  {
    id: 'properties',
    labelKey: 'properties',
    group: 'select',
    icon: Info,
    shortcut: 'properties',
    isApplicable: one,
    run: (c) => c.openProperties(),
  },
  {
    id: 'folderProperties',
    labelKey: 'folderProperties',
    group: 'select',
    icon: Info,
    isApplicable: always,
    run: (c) => c.openFolderProperties(),
  },
  {
    id: 'delete',
    labelKey: 'delete',
    group: 'fileops',
    icon: Trash2,
    shortcut: 'delete',
    destructive: true,
    isApplicable: some,
    run: (c) => c.deleteItems(),
  },
  {
    id: 'deletePermanent',
    labelKey: 'deletePermanent',
    group: 'fileops',
    icon: Trash2,
    shortcut: 'deletePermanent',
    destructive: true,
    isApplicable: some,
    run: (c) => c.deletePermanent(),
  },
  // --- Recycle Bin (Step 10a) ---
  {
    id: 'restore',
    labelKey: 'restore',
    group: 'fileops',
    icon: ArchiveRestore,
    isApplicable: some,
    run: (c) => c.restore?.(),
  },
  {
    id: 'restoreTo',
    labelKey: 'restoreTo',
    group: 'fileops',
    icon: FolderOutput,
    isApplicable: some,
    run: (c) => c.restoreTo?.(),
  },
  {
    id: 'purge',
    labelKey: 'purge',
    group: 'fileops',
    icon: Trash2,
    destructive: true,
    isApplicable: some,
    run: (c) => c.purge?.(),
  },
  // --- background ---
  {
    id: 'new',
    labelKey: 'new',
    group: 'create',
    icon: Plus,
    submenu: true,
    isApplicable: always,
    run: noop,
  },
  {
    id: 'newFolder',
    labelKey: 'newFolder',
    group: 'create',
    icon: FolderPlus,
    shortcut: 'newFolder',
    isApplicable: always,
    run: (c) => c.openNew('folder'),
  },
  {
    id: 'newFile',
    labelKey: 'newFile',
    group: 'create',
    icon: FilePlus2,
    shortcut: 'newFile',
    isApplicable: always,
    run: (c) => c.openNew('file'),
  },
  {
    id: 'newFromTemplate',
    labelKey: 'newFromTemplate',
    group: 'create',
    icon: FilePlus2,
    isApplicable: always,
    run: (c) => c.openNew('template'),
  },
  {
    id: 'newSymlink',
    labelKey: 'newSymlink',
    group: 'create',
    icon: LinkIcon,
    isApplicable: always,
    run: (c) => c.openNew('symlink'),
  },
  {
    id: 'uploadFile',
    labelKey: 'uploadFile',
    group: 'transfer',
    icon: Upload,
    shortcut: 'upload',
    isApplicable: always,
    run: (c) => c.uploadFile?.(),
  },
  {
    id: 'uploadFolder',
    labelKey: 'uploadFolder',
    group: 'transfer',
    icon: FolderUp,
    isApplicable: always,
    run: (c) => c.uploadFolder?.(),
  },
  {
    id: 'remoteDownload',
    labelKey: 'remoteDownload',
    group: 'transfer',
    icon: Globe,
    available: false,
    isApplicable: always,
    run: noop,
  },
  {
    id: 'refresh',
    labelKey: 'refresh',
    group: 'navigate',
    icon: RotateCw,
    shortcut: 'refresh',
    isApplicable: always,
    run: (c) => c.refresh(),
  },
  {
    id: 'selectAll',
    labelKey: 'selectAll',
    group: 'select',
    icon: SquareMousePointer,
    shortcut: 'selectAll',
    isApplicable: (c) => c.total > 0,
    run: (c) => c.selectAll(),
  },
  {
    id: 'invertSelection',
    labelKey: 'invertSelection',
    group: 'select',
    icon: SquareDashedMousePointer,
    shortcut: 'invertSelection',
    isApplicable: (c) => c.total > 0,
    run: (c) => c.invertSelection(),
  },
  {
    id: 'clearSelection',
    labelKey: 'clearSelection',
    group: 'select',
    icon: CopyMinus,
    shortcut: 'escape',
    isApplicable: some,
    run: (c) => c.clearSelection(),
  },
  {
    id: 'view',
    labelKey: 'view',
    group: 'view',
    icon: LayoutGrid,
    submenu: true,
    isApplicable: always,
    run: noop,
  },
  {
    id: 'sort',
    labelKey: 'sort',
    group: 'view',
    icon: ArrowDownUp,
    submenu: true,
    isApplicable: always,
    run: noop,
  },
  {
    id: 'columns',
    labelKey: 'columns',
    group: 'view',
    icon: Columns3,
    submenu: true,
    isApplicable: always,
    run: (c) => c.openColumns(),
  },
  // --- app / navigation ---
  {
    id: 'newTab',
    labelKey: 'newTab',
    group: 'navigate',
    icon: Plus,
    shortcut: 'newTab',
    isApplicable: always,
    run: (c) => c.openTab(c.path),
  },
  {
    id: 'closeTab',
    labelKey: 'closeTab',
    group: 'navigate',
    icon: X,
    shortcut: 'closeTab',
    isApplicable: always,
    run: (c) => c.closeTab(),
  },
  {
    id: 'viewDetails',
    labelKey: 'viewDetails',
    group: 'view',
    icon: LayoutList,
    shortcut: 'viewDetails',
    isApplicable: always,
    run: noop,
  },
  {
    id: 'previewPanel',
    labelKey: 'previewPanel',
    group: 'view',
    icon: PanelRight,
    shortcut: 'previewPanel',
    isApplicable: always,
    run: (c) => c.togglePreview(),
  },
  {
    id: 'theme',
    labelKey: 'theme',
    group: 'app',
    icon: SunMoon,
    isApplicable: always,
    run: (c) => c.toggleTheme(),
  },
  {
    id: 'filter',
    labelKey: 'filter',
    group: 'navigate',
    icon: Search,
    shortcut: 'filter',
    isApplicable: always,
    run: (c) => c.focusFilter(),
  },
  {
    id: 'help',
    labelKey: 'help',
    group: 'app',
    icon: Keyboard,
    shortcut: 'help',
    isApplicable: always,
    run: (c) => c.openHelp(),
  },
]

const BY_ID = new Map(ACTIONS.map((a) => [a.id, a]))

export function actionById(id: string): ActionDef | undefined {
  return BY_ID.get(id)
}

export function isAvailable(a: ActionDef): boolean {
  return a.available !== false
}

/** Actions a flat surface (palette, toolbar "more") can run right now. */
export function applicableActions(ctx: ActionContext): ActionDef[] {
  return ACTIONS.filter((a) => isAvailable(a) && !a.submenu && a.isApplicable(ctx))
}

/** Context-menu matrix (SPEC §9.5), `|` = separator. Unavailable or inapplicable
 *  actions are dropped; empty groups collapse. */
export type MenuContext = 'file' | 'folder' | 'multi' | 'background' | 'trash'

export const MENU_MATRIX: Record<MenuContext, string[]> = {
  file: [
    'open',
    'openWith',
    'quickLook',
    '|',
    'download',
    'share',
    'copyShareLink',
    '|',
    'favorite',
    'unfavorite',
    'pin',
    'unpin',
    'note',
    'tagColor',
    '|',
    'copy',
    'cut',
    'duplicate',
    'rename',
    'moveTo',
    'copyTo',
    '|',
    'compress',
    'extract',
    'browseArchive',
    '|',
    'permissions',
    'symlink',
    'checksum',
    'versions',
    '|',
    'copyPath',
    'openLocation',
    'properties',
    '|',
    'delete',
  ],
  folder: [
    'open',
    'openInNewTab',
    'openInNewWindow',
    'downloadZip',
    '|',
    'share',
    'favorite',
    'unfavorite',
    'pin',
    'unpin',
    'note',
    'tagColor',
    '|',
    'copy',
    'cut',
    'duplicate',
    'rename',
    'moveTo',
    'copyTo',
    '|',
    'compress',
    'folderSize',
    'uploadHere',
    'searchHere',
    '|',
    'permissions',
    'copyPath',
    'properties',
    '|',
    'delete',
  ],
  multi: [
    'downloadZip',
    '|',
    'copy',
    'cut',
    'moveTo',
    'copyTo',
    '|',
    'compress',
    'batchRename',
    'permissions',
    '|',
    'favorite',
    'unfavorite',
    'tagColor',
    '|',
    'delete',
  ],
  background: [
    'new',
    'uploadFile',
    'uploadFolder',
    'remoteDownload',
    '|',
    'paste',
    'refresh',
    'selectAll',
    '|',
    'view',
    'sort',
    'columns',
    '|',
    'folderProperties',
  ],
  // Recycle Bin item(s) (SPEC §8.6): restore, restore elsewhere, purge.
  trash: ['restore', 'restoreTo', '|', 'purge'],
}

/** Which context a selection belongs to (null = nothing selected → background). */
export function menuContextFor(
  ctx: Pick<ActionContext, 'selection' | 'selectionCount'>,
): MenuContext {
  if (ctx.selectionCount === 0) return 'background'
  if (ctx.selectionCount > 1 || ctx.selection.length !== 1) return 'multi'
  return ctx.selection[0]!.isDir ? 'folder' : 'file'
}

export function menuItems(context: MenuContext, ctx: ActionContext): ActionDef[][] {
  const groups: ActionDef[][] = []
  let cur: ActionDef[] = []
  for (const id of MENU_MATRIX[context]) {
    if (id === '|') {
      if (cur.length) groups.push(cur)
      cur = []
      continue
    }
    const a = BY_ID.get(id)
    if (!a) throw new Error(`unknown action ${id} in menu matrix`)
    if (!isAvailable(a) || !a.isApplicable(ctx)) continue
    cur.push(a)
  }
  if (cur.length) groups.push(cur)
  return groups
}

/** Toolbar layout (SPEC §9.3): primary icons always rendered (disabled when
 *  not applicable); the rest of the applicable actions go under "⋯". */
export const TOOLBAR_PRIMARY = ['copy', 'cut', 'paste', 'rename', 'delete']
export const TOOLBAR_MORE = [
  'moveTo',
  'copyTo',
  'duplicate',
  'batchRename',
  'favorite',
  'unfavorite',
  'pin',
  'unpin',
  'note',
  'permissions',
  'symlink',
  'copyPath',
  'properties',
  'undo',
  'selectAll',
  'invertSelection',
]

// The browser screen publishes its context so global surfaces (palette, top bar)
// can run actions without prop drilling.
let current: ActionContext | null = null
const listeners = new Set<() => void>()

export function setActionContext(ctx: ActionContext | null): void {
  current = ctx
  for (const l of listeners) l()
}
export function getActionContext(): ActionContext | null {
  return current
}
export function subscribeActionContext(cb: () => void): () => void {
  listeners.add(cb)
  return () => listeners.delete(cb)
}
