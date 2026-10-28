import { useEffect, useMemo, useState } from 'react'
import type { TransferOp } from '@/features/file-ops/api'
import { dropTargetProps, readDragPayload } from '@/features/file-ops/dnd'
import { resolvePaneDrop } from './paneDrop'

/** Drop target for a pane's whole listing area (Step 23b, SPEC §9.3): drops go
 *  to the pane's folder through the regular `dropTargetProps` handler, and while
 *  items from another folder hover it the op ("Pindahkan/Salin ke …") is shown. */
export function usePaneDrop(dir: string, isFolderPath: (path: string) => boolean) {
  const [op, setOp] = useState<TransferOp | null>(null)
  useEffect(() => {
    if (!op) return
    // the grid stops the drop event and the dragged row may unmount (no dragend)
    const reset = () => setOp(null)
    window.addEventListener('drop', reset, true)
    window.addEventListener('dragend', reset, true)
    return () => {
      window.removeEventListener('drop', reset, true)
      window.removeEventListener('dragend', reset, true)
    }
  }, [op])
  const props = useMemo(() => {
    const base = dropTargetProps({ dir })
    return {
      onDragEnter: base.onDragEnter,
      onDragOver: (e: React.DragEvent) => {
        base.onDragOver(e)
        const row =
          e.target instanceof Element ? e.target.closest<HTMLElement>('[data-path]') : null
        const rowPath = row?.dataset.path
        const res = resolvePaneDrop({
          paths: readDragPayload(null)?.paths ?? null,
          dir,
          overFolderRow: !!rowPath && isFolderPath(rowPath),
          copy: e.ctrlKey || e.metaKey,
        })
        setOp(res?.op ?? null)
      },
      onDragLeave: (e: React.DragEvent) => {
        base.onDragLeave(e)
        const to = e.relatedTarget
        if (to instanceof Node && e.currentTarget.contains(to)) return
        setOp(null)
      },
      onDrop: (e: React.DragEvent) => {
        setOp(null)
        base.onDrop(e)
      },
    }
  }, [dir, isFolderPath])
  return { props, op }
}
