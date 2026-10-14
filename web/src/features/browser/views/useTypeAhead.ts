import { useRef } from 'react'

const RESET_MS = 1000

/** Type-ahead shared by all views: printable keys jump to the next item whose
 *  name starts with the typed buffer (a single/repeated character cycles from
 *  the active item, a longer prefix searches from the top). */
export function useTypeAhead(
  names: () => string[],
  active: () => number,
  move: (i: number) => void,
) {
  const state = useRef<{ buf: string; at: number }>({ buf: '', at: 0 })
  return (ch: string) => {
    const now = Date.now()
    const s = state.current
    const buf = now - s.at < RESET_MS ? s.buf + ch : ch
    state.current = { buf, at: now }
    const needle = buf.toLowerCase()
    const repeated = buf.length > 1 && [...buf].every((c) => c === buf[0])
    const list = names()
    const start = repeated || buf.length === 1 ? active() + 1 : 0
    for (let k = 0; k < list.length; k++) {
      const i = (start + k) % list.length
      if (list[i]!.toLowerCase().startsWith(repeated ? needle[0]! : needle)) {
        move(i)
        return
      }
    }
  }
}
