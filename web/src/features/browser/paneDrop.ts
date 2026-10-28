import type { TransferOp } from '@/features/file-ops/api'
import { canDropOn, parentOf } from '@/features/file-ops/dnd'

/** Drop-target resolution for a pane's listing area (SPEC §9.3 dual-pane,
 *  Step 23b). Pure so it can be unit-tested without DOM drag events. */

/** The dragged paths that would actually change folder when dropped in `dir`
 *  (items already in `dir` are a no-op: no request, no conflict dialog). */
export function transferablePaths(paths: string[], dir: string): string[] {
  return paths.filter((p) => parentOf(p) !== dir)
}

export interface PaneDropInput {
  /** paths of the in-app drag (null: OS files or a drag from another tab) */
  paths: string[] | null
  /** the pane's current folder */
  dir: string
  /** the pointer is over a folder row, which takes the drop itself */
  overFolderRow: boolean
  /** Ctrl/⌘ held: copy instead of move (SPEC §8.5) */
  copy: boolean
}

export interface PaneDrop {
  op: TransferOp
  paths: string[]
}

/** What a drop on the pane's listing area does; null = nothing (no badge). */
export function resolvePaneDrop(input: PaneDropInput): PaneDrop | null {
  if (!input.paths || input.paths.length === 0 || input.overFolderRow) return null
  if (!canDropOn(input.paths, input.dir)) return null
  const paths = transferablePaths(input.paths, input.dir)
  if (paths.length === 0) return null
  return { op: input.copy ? 'copy' : 'move', paths }
}
