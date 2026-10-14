/** HTML5 drag & drop helpers (SPEC §8.5 "Drag & drop"): rows carry their
 *  paths in a custom MIME type; folder rows, breadcrumb segments and the
 *  sidebar accept them (Ctrl = copy). Drops from the OS are forwarded to the
 *  upload hook (Step 12a; a toast until then). Native DnD was chosen over
 *  dnd-kit because it is the only API that receives OS files. */
import type { TransferOp } from './api'

export const DND_MIME = 'application/x-fm-paths'

export interface DragPayload {
  paths: string[]
  from: string
}

let current: DragPayload | null = null

/** Attach the payload to a dragstart event; keeps a module copy because
 *  `getData` is not readable during dragover in Chromium. */
export function startItemDrag(
  e: { dataTransfer: DataTransfer | null },
  payload: DragPayload,
  label: string,
): void {
  const dt = e.dataTransfer
  if (!dt) return
  current = payload
  dt.effectAllowed = 'copyMove'
  dt.setData(DND_MIME, JSON.stringify(payload))
  dt.setData('text/plain', payload.paths.join('\n'))
  if (typeof document !== 'undefined' && typeof dt.setDragImage === 'function') {
    const ghost = document.createElement('div')
    ghost.textContent = label
    ghost.setAttribute('aria-hidden', 'true')
    ghost.style.cssText =
      'position:fixed;top:-1000px;left:-1000px;padding:4px 10px;border-radius:6px;font:500 12px system-ui,sans-serif;background:var(--accent);color:#fff;box-shadow:0 2px 8px rgba(0,0,0,.3);pointer-events:none'
    document.body.appendChild(ghost)
    dt.setDragImage(ghost, 12, 12)
    setTimeout(() => ghost.remove(), 0)
  }
}

export function endItemDrag(): void {
  current = null
}

export function hasItemDrag(dt: DataTransfer | null): boolean {
  if (!dt) return false
  return Array.from(dt.types).includes(DND_MIME) || current !== null
}

export function hasOsFiles(dt: DataTransfer | null): boolean {
  if (!dt) return false
  return Array.from(dt.types).includes('Files')
}

export function readDragPayload(dt: DataTransfer | null): DragPayload | null {
  if (dt) {
    try {
      const raw = dt.getData(DND_MIME)
      if (raw) {
        const v = JSON.parse(raw) as Partial<DragPayload>
        if (Array.isArray(v.paths)) {
          return {
            paths: v.paths.filter((p): p is string => typeof p === 'string'),
            from: typeof v.from === 'string' ? v.from : '/',
          }
        }
      }
    } catch {
      /* fall through to the module copy */
    }
  }
  return current
}

export function dropOp(e: { ctrlKey: boolean; metaKey: boolean }): TransferOp {
  return e.ctrlKey || e.metaKey ? 'copy' : 'move'
}

/** A folder can receive `paths` unless it is one of them or inside one of them. */
export function canDropOn(paths: string[], targetDir: string): boolean {
  for (const p of paths) {
    if (p === targetDir || targetDir.startsWith(p.endsWith('/') ? p : `${p}/`)) return false
  }
  return true
}

export function parentOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i <= 0 ? '/' : path.slice(0, i)
}

/** Drop handler published by the browser screen (consumed by the sidebar and
 *  breadcrumb, which live outside it). */
export type DropHandler = (payload: DragPayload, targetDir: string, op: TransferOp) => void
export type OsDropHandler = (files: DataTransfer, targetDir: string) => void

let dropHandler: DropHandler | null = null
let osDropHandler: OsDropHandler | null = null

export function setDropHandlers(h: { items: DropHandler; os: OsDropHandler } | null): void {
  dropHandler = h?.items ?? null
  osDropHandler = h?.os ?? null
}

/** Dropping items on the sidebar "Favorit" entry adds them to the favourites (SPEC §8.7). */
export type FavoriteDropHandler = (paths: string[]) => void
let favoriteDropHandler: FavoriteDropHandler | null = null
export function setFavoriteDropHandler(h: FavoriteDropHandler | null): void {
  favoriteDropHandler = h
}

/** Event handlers for the favourites drop target (accepts item drags only). */
export function favoriteDropTargetProps(onOver?: (over: boolean) => void) {
  const accepts = (dt: DataTransfer | null) => hasItemDrag(dt) && !!favoriteDropHandler
  return {
    onDragEnter: (e: React.DragEvent) => {
      if (!accepts(e.dataTransfer)) return
      e.preventDefault()
      onOver?.(true)
    },
    onDragOver: (e: React.DragEvent) => {
      if (!accepts(e.dataTransfer)) return
      e.preventDefault()
      e.dataTransfer.dropEffect = 'link'
      onOver?.(true)
    },
    onDragLeave: (e: React.DragEvent) => {
      const to = e.relatedTarget
      if (to instanceof Node && e.currentTarget.contains(to)) return
      onOver?.(false)
    },
    onDrop: (e: React.DragEvent) => {
      onOver?.(false)
      const payload = readDragPayload(e.dataTransfer)
      if (!payload || !favoriteDropHandler) return
      e.preventDefault()
      e.stopPropagation()
      favoriteDropHandler(payload.paths)
    },
  }
}

export interface DropTargetOptions {
  /** virtual path of the folder represented by the element */
  dir: string
  /** called with the dir when the pointer enters/leaves (for highlighting) */
  onOver?: (dir: string | null) => void
}

/** Returns true when the drag can be accepted by `dir` (items or OS files). */
export function acceptsDrag(dt: DataTransfer | null, dir: string): boolean {
  if (hasOsFiles(dt)) return true
  if (!hasItemDrag(dt)) return false
  const payload = current
  if (!payload) return true // another browser tab: decide on drop
  return canDropOn(payload.paths, dir)
}

/** Event handlers for a folder drop target (rows, crumbs, sidebar entries). */
export function dropTargetProps(opts: DropTargetOptions) {
  return {
    onDragEnter: (e: React.DragEvent) => {
      if (!acceptsDrag(e.dataTransfer, opts.dir)) return
      e.preventDefault()
      opts.onOver?.(opts.dir)
    },
    onDragOver: (e: React.DragEvent) => {
      if (!acceptsDrag(e.dataTransfer, opts.dir)) return
      e.preventDefault()
      e.dataTransfer.dropEffect = hasOsFiles(e.dataTransfer) ? 'copy' : dropOp(e)
      opts.onOver?.(opts.dir)
    },
    onDragLeave: (e: React.DragEvent) => {
      // ignore moves between children of the same target
      const to = e.relatedTarget
      if (to instanceof Node && e.currentTarget.contains(to)) return
      opts.onOver?.(null)
    },
    onDrop: (e: React.DragEvent) => {
      opts.onOver?.(null)
      if (hasOsFiles(e.dataTransfer)) {
        e.preventDefault()
        osDropHandler?.(e.dataTransfer, opts.dir)
        return
      }
      const payload = readDragPayload(e.dataTransfer)
      if (!payload) return
      e.preventDefault()
      e.stopPropagation()
      if (!canDropOn(payload.paths, opts.dir)) return
      dropHandler?.(payload, opts.dir, dropOp(e))
    },
  }
}

/** Auto-scroll a scroll container while dragging near its top/bottom edge. */
export function autoScrollOnDrag(el: HTMLElement, clientY: number, edge = 28, step = 14): void {
  const b = el.getBoundingClientRect()
  if (clientY > b.bottom - edge) el.scrollTop += step
  else if (clientY < b.top + edge) el.scrollTop -= step
}
