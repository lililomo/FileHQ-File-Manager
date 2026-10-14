import { create } from 'zustand'
import type { Item } from '@/features/browser/api'
import type { PrecheckItem } from '@/features/upload/api'
import type { Conflict, ConflictPolicy, TransferOp } from './api'

/** The file-operation dialogs are opened imperatively (from the menu,
 *  keyboard or the operations hook) through this store; `FileOpsDialogs`
 *  renders whichever one is open. One dialog at a time. */
export type NewItemMode = 'file' | 'folder' | 'template' | 'symlink'

export type FileOpsDialog =
  | {
      kind: 'conflict'
      op: TransferOp
      conflicts: Conflict[]
      resolve: (perItem: Record<string, ConflictPolicy> | null) => void
    }
  | { kind: 'moveCopy'; op: TransferOp; paths: string[]; from: string }
  | { kind: 'batchRename'; paths: string[]; dir: string }
  | { kind: 'new'; mode: NewItemMode; dir: string; target?: string; template?: string }
  | { kind: 'permissions'; items: Item[] }
  | { kind: 'deleteConfirm'; paths: string[]; resolve: (ok: boolean) => void }
  | { kind: 'deletePermanent'; paths: string[]; resolve: (ok: boolean) => void }
  | { kind: 'uploadPrecheck'; info: UploadPrecheckInfo; resolve: (proceed: boolean) => void }
  | { kind: 'remoteDownload'; dir: string }

/** What the precheck dialog shows before an upload starts (SPEC §8.8 step 2). */
export interface UploadPrecheckInfo {
  rejected: PrecheckItem[]
  /** files that passed (ok + conflict) */
  accepted: number
  /** files that still fit when the quota is short */
  fits: number
  needed: number
  free: number
  limit: number
  maxUploadSize: number
  quotaReason?: 'quota' | 'disk'
}

export interface FileOpsDialogState {
  dialog: FileOpsDialog | null
  open: (d: FileOpsDialog) => void
  close: () => void
}

export const useFileOpsDialogs = create<FileOpsDialogState>((set, get) => ({
  dialog: null,
  open: (d) => {
    const cur = get().dialog
    // a pending promise-based dialog must not be left dangling
    if (cur && 'resolve' in cur) (cur.resolve as (v: null | false) => void)(null as never)
    set({ dialog: d })
  },
  close: () => set({ dialog: null }),
}))

/** Promise helper for the dialogs that answer a question. */
export function askConflicts(
  op: TransferOp,
  conflicts: Conflict[],
): Promise<Record<string, ConflictPolicy> | null> {
  return new Promise((resolve) => {
    useFileOpsDialogs.getState().open({
      kind: 'conflict',
      op,
      conflicts,
      resolve: (v) => {
        useFileOpsDialogs.getState().close()
        resolve(v)
      },
    })
  })
}

export function askDelete(
  kind: 'deleteConfirm' | 'deletePermanent',
  paths: string[],
): Promise<boolean> {
  return new Promise((resolve) => {
    useFileOpsDialogs.getState().open({
      kind,
      paths,
      resolve: (ok) => {
        useFileOpsDialogs.getState().close()
        resolve(!!ok)
      },
    })
  })
}

/** Last undoable operation (SPEC §8.5 "Undo": button shown 10 s, token lives 60 s). */
export interface UndoEntry {
  token: string
  at: number
  toastId: string | number
  label: string
}

export const UNDO_WINDOW_MS = 10_000

interface UndoState {
  entry: UndoEntry | null
  set: (e: UndoEntry | null) => void
}

export const useUndoStore = create<UndoState>((set) => ({
  entry: null,
  set: (entry) => set({ entry }),
}))

export function canUndoNow(entry: UndoEntry | null, now = Date.now()): boolean {
  return !!entry && now - entry.at < UNDO_WINDOW_MS
}

/** Move keyboard focus back to the file grid after a dialog closes (PROMPT 9b AC). */
export function focusGrid(): void {
  if (typeof document === 'undefined') return
  const grid = document.querySelector<HTMLElement>('[data-testid="browser"] [role="grid"]')
  grid?.focus()
}

/** Rejections / quota shortfall before an upload; resolves true to continue
 *  with the files that passed (or fit). */
export function askUploadPrecheck(info: UploadPrecheckInfo): Promise<boolean> {
  return new Promise((resolve) => {
    useFileOpsDialogs.getState().open({
      kind: 'uploadPrecheck',
      info,
      resolve: (v) => {
        useFileOpsDialogs.getState().close()
        resolve(!!v)
      },
    })
  })
}
