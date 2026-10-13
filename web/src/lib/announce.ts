/** Announce a message through the global live regions (SPEC §9.9). */
export function announce(message: string, assertive = false): void {
  if (typeof document === 'undefined') return
  const el = document.getElementById(assertive ? 'live-assertive' : 'live-polite')
  if (!el) return
  el.textContent = ''
  window.setTimeout(() => {
    el.textContent = message
  }, 50)
}
