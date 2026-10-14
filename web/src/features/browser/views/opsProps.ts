import type { ReactNode } from 'react'
import type { Item } from '../api'

/** Props shared by every view for the Step 9b interactions: inline rename
 *  slot, faded "cut" items, slow-click rename, drag source and drop targets. */
export interface ViewOpsProps {
  /** Path currently being renamed inline; the view renders `renderRename` in place of the name. */
  renamingPath?: string | null
  renderRename?: (item: Item) => ReactNode
  /** Items in the cut clipboard (rendered semi-transparent). */
  cutPaths?: ReadonlySet<string>
  /** A slow second click on the name of the only selected item (PROMPT 9b task 7). */
  onSlowClick?: (item: Item) => void
  /** Paths carried by a drag that starts on `item` (the selection, or the item alone). */
  dragPaths?: (item: Item) => string[]
  /** Enables drag & drop (default true). */
  dnd?: boolean
  /** Inline "Catatan" editing in the Details view (Step 10b). */
  onNoteSave?: ((path: string, note: string) => Promise<boolean>) | undefined
}
