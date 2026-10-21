/** JSON tree model for the JSON viewer (SPEC §8.14 JsonViewer): nodes are
 *  addressed by JSON Pointer, the visible rows are a flat list of the
 *  expanded part (so the view can be virtualised), key search walks the
 *  whole document once. Pure functions, unit-tested. */

export type JsonValue = null | boolean | number | string | JsonValue[] | { [k: string]: JsonValue }

export interface JsonRow {
  /** JSON Pointer ("" = root) */
  ptr: string
  /** property name or array index; null for the root */
  key: string | number | null
  value: JsonValue
  depth: number
  container: boolean
  /** number of children (containers) */
  size: number
}

export function isContainer(v: JsonValue): v is JsonValue[] | { [k: string]: JsonValue } {
  return v !== null && typeof v === 'object'
}

const esc = (k: string | number) => String(k).replace(/~/g, '~0').replace(/\//g, '~1')

export function childPtr(ptr: string, key: string | number): string {
  return `${ptr}/${esc(key)}`
}

function entries(v: JsonValue): [string | number, JsonValue][] {
  if (Array.isArray(v)) return v.map((x, i) => [i, x])
  if (isContainer(v)) return Object.entries(v)
  return []
}

/** Visible rows: the root plus the children of every expanded container.
 *  Iterative (explicit stack, document order): a pathologically nested
 *  document must not blow the call stack (review D). */
export function flatten(root: JsonValue, expanded: ReadonlySet<string>): JsonRow[] {
  const out: JsonRow[] = []
  const stack: { value: JsonValue; ptr: string; key: string | number | null; depth: number }[] = [
    { value: root, ptr: '', key: null, depth: 0 },
  ]
  while (stack.length) {
    const { value, ptr, key, depth } = stack.pop()!
    const container = isContainer(value)
    const kids = container ? entries(value) : []
    out.push({ ptr, key, value, depth, container, size: kids.length })
    if (container && expanded.has(ptr)) {
      for (let i = kids.length - 1; i >= 0; i--) {
        const [k, v] = kids[i]!
        stack.push({ value: v, ptr: childPtr(ptr, k), key: k, depth: depth + 1 })
      }
    }
  }
  return out
}

/** Pointers of every container up to `depth` levels (initial expansion). */
export function expandToDepth(root: JsonValue, depth: number): Set<string> {
  const out = new Set<string>()
  const stack: [JsonValue, string, number][] = [[root, '', 0]]
  while (stack.length) {
    const [value, ptr, d] = stack.pop()!
    if (!isContainer(value) || d >= depth) continue
    out.add(ptr)
    for (const [k, v] of entries(value)) stack.push([v, childPtr(ptr, k), d + 1])
  }
  return out
}

/** Every container pointer (expand all), bounded so a huge document stays usable. */
export function allContainers(root: JsonValue, limit = 50_000): Set<string> {
  const out = new Set<string>()
  const stack: [JsonValue, string][] = [[root, '']]
  while (stack.length && out.size < limit) {
    const [v, p] = stack.pop()!
    if (!isContainer(v)) continue
    out.add(p)
    for (const [k, c] of entries(v)) if (isContainer(c)) stack.push([c, childPtr(p, k)])
  }
  return out
}

/** Pointers whose key contains `query` (case-insensitive), in document order
 *  (iterative, see flatten). */
export function searchKeys(root: JsonValue, query: string, limit = 1000): string[] {
  const q = query.trim().toLowerCase()
  if (!q) return []
  const out: string[] = []
  // pre-order walk with an explicit stack of (children, cursor)
  const stack: { kids: [string | number, JsonValue][]; i: number; ptr: string }[] = [
    { kids: entries(root), i: 0, ptr: '' },
  ]
  while (stack.length && out.length < limit) {
    const top = stack[stack.length - 1]!
    if (top.i >= top.kids.length) {
      stack.pop()
      continue
    }
    const [k, v] = top.kids[top.i++]!
    const p = childPtr(top.ptr, k)
    if (typeof k === 'string' && k.toLowerCase().includes(q)) out.push(p)
    if (isContainer(v)) stack.push({ kids: entries(v), i: 0, ptr: p })
  }
  return out
}

/** Pointers of the ancestors of `ptr` (to expand so it becomes visible). */
export function ancestors(ptr: string): string[] {
  const out = ['']
  const parts = ptr.split('/').slice(1)
  let cur = ''
  for (let i = 0; i < parts.length - 1; i++) {
    cur += `/${parts[i]}`
    out.push(cur)
  }
  return out
}

/** JSONPath-style display of a pointer: `$.a[0]["b c"]`. */
export function displayPath(ptr: string): string {
  if (!ptr) return '$'
  return (
    '$' +
    ptr
      .split('/')
      .slice(1)
      .map((raw) => raw.replace(/~1/g, '/').replace(/~0/g, '~'))
      .map((k) =>
        /^\d+$/.test(k)
          ? `[${k}]`
          : /^[A-Za-z_$][\w$]*$/.test(k)
            ? `.${k}`
            : `[${JSON.stringify(k)}]`,
      )
      .join('')
  )
}
