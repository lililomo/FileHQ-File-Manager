/** Where the persistent mini-player renders (SPEC §8.14, PROMPT 17b: "di
 *  bawah layar, di atas status bar"). The player is mounted once in AppShell
 *  so it outlives route changes; a page that owns a status bar (the file
 *  browser) registers a slot element right above that bar and the
 *  mini-player portals into it. Without a slot it stays AppShell's last row. */
import { useSyncExternalStore } from 'react'

let slot: HTMLElement | null = null
const listeners = new Set<() => void>()

/** Ref callback for the slot element (`null` when the page unmounts). */
export function setMiniPlayerSlot(el: HTMLElement | null): void {
  if (el === slot) return
  slot = el
  for (const l of listeners) l()
}

function subscribe(l: () => void): () => void {
  listeners.add(l)
  return () => listeners.delete(l)
}

export function useMiniPlayerSlot(): HTMLElement | null {
  return useSyncExternalStore(
    subscribe,
    () => slot,
    () => null,
  )
}
