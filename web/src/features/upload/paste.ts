/** Ctrl+V with OS files/images on the clipboard uploads them to the open
 *  folder (SPEC §8.8); the application clipboard (copied paths) keeps its own
 *  shortcut when the OS clipboard carries no files. */
export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT'
}

export function clipboardHasFiles(dt: DataTransfer | null): boolean {
  if (!dt) return false
  if (dt.files && dt.files.length > 0) return true
  return Array.from(dt.items ?? []).some((it) => it.kind === 'file')
}

/** Installs the document paste listener; returns the disposer. */
export function installPasteUpload(
  getDest: () => string | null,
  upload: (dt: DataTransfer, dest: string) => void,
): () => void {
  const onPaste = (e: ClipboardEvent) => {
    if (isEditableTarget(e.target)) return
    const dt = e.clipboardData
    if (!clipboardHasFiles(dt)) return
    const dest = getDest()
    if (!dest) return
    e.preventDefault()
    upload(dt!, dest)
  }
  document.addEventListener('paste', onPaste)
  return () => document.removeEventListener('paste', onPaste)
}
