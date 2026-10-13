import { joinPath, ROOT, splitPath } from './path'

export interface Segment {
  name: string
  path: string
}

export function segmentsOf(path: string): Segment[] {
  const out: Segment[] = [{ name: '', path: ROOT }]
  let acc = ''
  for (const part of splitPath(path)) {
    acc = joinPath(acc || ROOT, part)
    out.push({ name: part, path: acc })
  }
  return out
}

/** Collapse rule (SPEC §9.2): keep the root, the last `keepTail` segments, and
 *  fold the middle into an ellipsis once there are more than `max` segments. */
export function collapseSegments(
  segments: Segment[],
  max = 4,
  keepTail = 2,
): { head: Segment[]; hidden: Segment[]; tail: Segment[] } {
  if (segments.length <= max) return { head: segments, hidden: [], tail: [] }
  const head = segments.slice(0, 1)
  const tail = segments.slice(segments.length - keepTail)
  const hidden = segments.slice(1, segments.length - keepTail)
  return { head, hidden, tail }
}
