import { useEffect } from 'react'
import { pushScope } from '@/lib/shortcuts'
import { BatchRenameDialog } from './BatchRenameDialog'
import { ConflictDialog } from './ConflictDialog'
import { DeleteConfirmDialog, DeletePermanentDialog } from './DeleteDialog'
import { MoveCopyDialog } from './MoveCopyDialog'
import { NewItemDialog } from './NewItemDialog'
import { PermissionDialog } from './PermissionDialog'
import { PrecheckDialog } from '@/features/upload/PrecheckDialog'
import { RemoteDownloadDialog } from '@/features/remote-download/RemoteDownloadDialog'
import { focusGrid, useFileOpsDialogs } from './store'
import type { FileOps } from './useFileOps'

/** Mounts whichever file-operation dialog is open (see `store.ts`). Focus
 *  returns to the file grid when a dialog closes (PROMPT 9b AC). */
export function FileOpsDialogs({ ops }: { ops: FileOps }) {
  const dialog = useFileOpsDialogs((s) => s.dialog)
  const close = useFileOpsDialogs((s) => s.close)
  useEffect(() => {
    if (!dialog) return
    return pushScope('dialog')
  }, [dialog])
  if (!dialog) return null
  const returnFocus = (e: Event) => {
    e.preventDefault()
    focusGrid()
  }
  switch (dialog.kind) {
    case 'conflict':
      return (
        <ConflictDialog
          op={dialog.op}
          conflicts={dialog.conflicts}
          onResolve={dialog.resolve}
          onCloseAutoFocus={returnFocus}
        />
      )
    case 'moveCopy':
      return (
        <MoveCopyDialog
          op={dialog.op}
          paths={dialog.paths}
          from={dialog.from}
          onClose={close}
          onSubmit={(to) => {
            close()
            void ops.transfer(dialog.op, dialog.paths, to)
          }}
          onCloseAutoFocus={returnFocus}
        />
      )
    case 'batchRename':
      return (
        <BatchRenameDialog
          paths={dialog.paths}
          onClose={close}
          onApply={ops.applyBatchRename}
          onCloseAutoFocus={returnFocus}
        />
      )
    case 'new':
      return (
        <NewItemDialog
          mode={dialog.mode}
          dir={dialog.dir}
          {...(dialog.target ? { target: dialog.target } : {})}
          {...(dialog.template ? { template: dialog.template } : {})}
          onClose={close}
          onSymlink={ops.symlink}
          onCloseAutoFocus={returnFocus}
        />
      )
    case 'permissions':
      return (
        <PermissionDialog
          items={dialog.items}
          onClose={close}
          onSubmit={ops.chmod}
          onCloseAutoFocus={returnFocus}
        />
      )
    case 'deleteConfirm':
      return (
        <DeleteConfirmDialog
          paths={dialog.paths}
          onResolve={dialog.resolve}
          onCloseAutoFocus={returnFocus}
        />
      )
    case 'deletePermanent':
      return (
        <DeletePermanentDialog
          paths={dialog.paths}
          onResolve={dialog.resolve}
          onCloseAutoFocus={returnFocus}
        />
      )
    case 'uploadPrecheck':
      return (
        <PrecheckDialog
          info={dialog.info}
          onResolve={dialog.resolve}
          onCloseAutoFocus={returnFocus}
        />
      )
    case 'remoteDownload':
      return (
        <RemoteDownloadDialog dir={dialog.dir} onClose={close} onCloseAutoFocus={returnFocus} />
      )
    default:
      return null
  }
}
