import type { LucideIcon } from 'lucide-react'
import {
  Columns3,
  CopyMinus,
  FilePlus2,
  FolderPlus,
  Info,
  Keyboard,
  LayoutList,
  PanelRight,
  Plus,
  RotateCw,
  Search,
  SquareDashedMousePointer,
  SquareMousePointer,
  SunMoon,
  X,
} from 'lucide-react'
import type { Item } from '@/features/browser/api'

/** Single source of truth for user actions (PROMPT 7b task 9): the command
 *  palette, toolbar and (Step 9b) context menu all read from this registry.
 *  Labels are i18n keys under `files:actions.*`; `shortcut` references
 *  `lib/shortcuts.ts` ids so tooltips and the "?" dialog stay in sync. */

export type ActionGroup = 'navigate' | 'create' | 'view' | 'select' | 'app'

export interface ActionContext {
  path: string
  selection: Item[]
  selectionCount: number
  total: number
  navigate: (path: string) => void
  refresh: () => void
  openNew: (kind: 'folder' | 'file') => void
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
  quickLook: () => void
  focusFilter: () => void
}

export interface ActionDef {
  id: string
  labelKey: string
  group: ActionGroup
  icon: LucideIcon
  shortcut?: string
  /** Hidden when false (SPEC §9.5: inapplicable actions are hidden, not disabled). */
  isApplicable: (ctx: ActionContext) => boolean
  run: (ctx: ActionContext) => void
}

const always = () => true

export const ACTIONS: ActionDef[] = [
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
    run: () => {},
  },
  {
    id: 'columns',
    labelKey: 'columns',
    group: 'view',
    icon: Columns3,
    isApplicable: always,
    run: (c) => c.openColumns(),
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
    isApplicable: (c) => c.selectionCount > 0,
    run: (c) => c.clearSelection(),
  },
  {
    id: 'quickLook',
    labelKey: 'quickLook',
    group: 'select',
    icon: Search,
    shortcut: 'quickLook',
    isApplicable: (c) => c.selection.length === 1,
    run: (c) => c.quickLook(),
  },
  {
    id: 'properties',
    labelKey: 'properties',
    group: 'select',
    icon: Info,
    shortcut: 'properties',
    isApplicable: (c) => c.selection.length === 1,
    run: (c) => c.openProperties(),
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

export function applicableActions(ctx: ActionContext): ActionDef[] {
  return ACTIONS.filter((a) => a.isApplicable(ctx))
}

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
