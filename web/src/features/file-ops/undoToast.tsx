import { toast } from 'sonner'
import { useUndoStore } from './store'
import { UNDO_WINDOW_MS } from './store'
import { UndoToastBody } from './UndoToastBody'

/** Shows the 10 s undo toast and records the token in `useUndoStore`. */
export function showUndoToast(message: string, token: string, onUndo: () => void): void {
  const id = toast.custom((tid) => <UndoToastBody id={tid} message={message} onUndo={onUndo} />, {
    duration: UNDO_WINDOW_MS,
    onAutoClose: () => {
      const cur = useUndoStore.getState().entry
      if (cur && cur.token === token) useUndoStore.getState().set(null)
    },
    onDismiss: () => {
      const cur = useUndoStore.getState().entry
      if (cur && cur.token === token) useUndoStore.getState().set(null)
    },
  })
  useUndoStore.getState().set({ token, at: Date.now(), toastId: id, label: message })
}
