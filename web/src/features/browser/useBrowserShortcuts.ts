import { useEffect } from 'react'

export interface BrowserShortcutHandlers {
  back: () => void
  forward: () => void
  up: () => void
  refresh: () => void
  editPath: () => void
}

function inEditable(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable
}

/** Global keyboard shortcuts of the browser screen (SPEC §9.6 / PROMPT 7a):
 *  Alt+←/→ back/forward, Alt+↑ or Backspace up, F5 refresh, Alt+D address bar. */
export function useBrowserShortcuts(h: BrowserShortcutHandlers): void {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return
      if (e.key === 'F5' && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
        e.preventDefault()
        h.refresh()
        return
      }
      if (e.altKey && !e.ctrlKey && !e.metaKey) {
        if (e.key === 'ArrowLeft') {
          e.preventDefault()
          h.back()
        } else if (e.key === 'ArrowRight') {
          e.preventDefault()
          h.forward()
        } else if (e.key === 'ArrowUp') {
          e.preventDefault()
          h.up()
        } else if (e.key === 'd' || e.key === 'D') {
          e.preventDefault()
          h.editPath()
        }
        return
      }
      if (e.key === 'Backspace' && !inEditable(e.target) && !e.ctrlKey && !e.metaKey && !e.altKey) {
        e.preventDefault()
        h.up()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [h])
}
