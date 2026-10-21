/** Keyboard shortcut registry (SPEC §9.6). One listener on `window`; handlers are
 *  registered per scope. Shortcuts are ignored while typing in an input/textarea/
 *  contenteditable (except those flagged `allowInInput`, e.g. Escape). Keys that the
 *  browser does not let pages intercept (Ctrl+N/T/W/L/D, …) are never used, and a
 *  unit test enforces it. */

export type ShortcutScope = 'global' | 'browser' | 'editor' | 'viewer' | 'video' | 'dialog'

export interface ShortcutDef {
  id: string
  /** `Ctrl+Shift+F`, `Alt+ArrowLeft`, `F2`, `?`, `Space`, `Delete`. `Ctrl` also matches ⌘ on macOS. */
  keys: string
  /** Alternative key strings that trigger the same action. */
  alt?: string[]
  scope: ShortcutScope
  /** i18n key under files:shortcuts.* */
  labelKey: string
  allowInInput?: boolean
  /** Listed in the "?" dialog but not yet wired (later steps). */
  planned?: boolean
}

/** Browser combinations that cannot be intercepted or hijack the browser (SPEC §9.6). */
export const FORBIDDEN_SHORTCUTS = [
  'Ctrl+N',
  'Ctrl+Shift+N',
  'Ctrl+T',
  'Ctrl+W',
  'Ctrl+L',
  'Ctrl+D',
  'Ctrl+Shift+C',
  'Ctrl+Shift+I',
  'Ctrl+Q',
]

export const SHORTCUTS: ShortcutDef[] = [
  // --- file browser ---
  { id: 'copy', keys: 'Ctrl+C', scope: 'browser', labelKey: 'copy' },
  { id: 'cut', keys: 'Ctrl+X', scope: 'browser', labelKey: 'cut' },
  { id: 'paste', keys: 'Ctrl+V', scope: 'browser', labelKey: 'paste' },
  { id: 'delete', keys: 'Delete', scope: 'browser', labelKey: 'delete' },
  {
    id: 'deletePermanent',
    keys: 'Shift+Delete',
    scope: 'browser',
    labelKey: 'deletePermanent',
  },
  { id: 'rename', keys: 'F2', scope: 'browser', labelKey: 'rename' },
  { id: 'selectAll', keys: 'Ctrl+A', scope: 'browser', labelKey: 'selectAll' },
  { id: 'invertSelection', keys: 'Ctrl+I', scope: 'browser', labelKey: 'invertSelection' },
  { id: 'open', keys: 'Enter', scope: 'browser', labelKey: 'open' },
  { id: 'quickLook', keys: 'Space', scope: 'browser', labelKey: 'quickLook' },
  { id: 'up', keys: 'Backspace', alt: ['Alt+ArrowUp'], scope: 'browser', labelKey: 'up' },
  { id: 'back', keys: 'Alt+ArrowLeft', scope: 'browser', labelKey: 'back' },
  { id: 'forward', keys: 'Alt+ArrowRight', scope: 'browser', labelKey: 'forward' },
  { id: 'filter', keys: 'Ctrl+F', scope: 'browser', labelKey: 'filter' },
  { id: 'contentSearch', keys: 'Ctrl+Shift+F', scope: 'browser', labelKey: 'contentSearch' },
  { id: 'palette', keys: 'Ctrl+K', scope: 'global', labelKey: 'palette', allowInInput: true },
  { id: 'newFolder', keys: 'Alt+N', scope: 'browser', labelKey: 'newFolder' },
  { id: 'newFile', keys: 'Alt+Shift+N', scope: 'browser', labelKey: 'newFile' },
  { id: 'upload', keys: 'Ctrl+U', scope: 'browser', labelKey: 'upload' },
  { id: 'viewDetails', keys: 'Ctrl+1', scope: 'browser', labelKey: 'viewDetails' },
  { id: 'viewList', keys: 'Ctrl+2', scope: 'browser', labelKey: 'viewList' },
  { id: 'viewTiles', keys: 'Ctrl+3', scope: 'browser', labelKey: 'viewTiles' },
  { id: 'viewLarge', keys: 'Ctrl+4', scope: 'browser', labelKey: 'viewLarge' },
  { id: 'viewXLarge', keys: 'Ctrl+5', scope: 'browser', labelKey: 'viewXLarge' },
  { id: 'undo', keys: 'Ctrl+Z', scope: 'browser', labelKey: 'undo' },
  { id: 'properties', keys: 'Alt+Enter', scope: 'browser', labelKey: 'properties' },
  { id: 'previewPanel', keys: 'Alt+P', scope: 'browser', labelKey: 'previewPanel' },
  { id: 'addressBar', keys: 'Alt+D', scope: 'browser', labelKey: 'addressBar' },
  { id: 'refresh', keys: 'F5', scope: 'browser', labelKey: 'refresh' },
  { id: 'escape', keys: 'Escape', scope: 'browser', labelKey: 'escape', allowInInput: true },
  { id: 'help', keys: '?', scope: 'global', labelKey: 'help' },
  {
    id: 'gridNav',
    keys: 'ArrowUp',
    alt: ['ArrowDown', 'Home', 'End', 'PageUp', 'PageDown'],
    scope: 'browser',
    labelKey: 'gridNav',
    planned: true,
  },
  { id: 'nextTab', keys: 'Ctrl+Tab', scope: 'browser', labelKey: 'nextTab' },
  { id: 'prevTab', keys: 'Ctrl+Shift+Tab', scope: 'browser', labelKey: 'prevTab' },
  {
    id: 'selectTab',
    keys: 'Alt+1',
    alt: ['Alt+2', 'Alt+3', 'Alt+4', 'Alt+5', 'Alt+6', 'Alt+7', 'Alt+8', 'Alt+9'],
    scope: 'browser',
    labelKey: 'selectTab',
  },
  { id: 'newTab', keys: 'Alt+T', scope: 'browser', labelKey: 'newTab' },
  { id: 'closeTab', keys: 'Alt+W', scope: 'browser', labelKey: 'closeTab' },
  // --- editor (Step 16) ---
  { id: 'editorSave', keys: 'Ctrl+S', scope: 'editor', allowInInput: true, labelKey: 'editorSave' },
  {
    id: 'editorSaveAll',
    keys: 'Ctrl+Shift+S',
    scope: 'editor',
    allowInInput: true,
    labelKey: 'editorSaveAll',
  },
  {
    id: 'editorCloseTab',
    keys: 'Alt+W',
    scope: 'editor',
    allowInInput: true,
    labelKey: 'editorCloseTab',
  },
  {
    id: 'editorGoToLine',
    keys: 'Ctrl+G',
    scope: 'editor',
    allowInInput: true,
    labelKey: 'editorGoToLine',
  },
  {
    id: 'editorPalette',
    keys: 'F1',
    scope: 'editor',
    allowInInput: true,
    labelKey: 'editorPalette',
  },
  {
    id: 'editorQuickOpen',
    keys: 'Ctrl+P',
    scope: 'editor',
    allowInInput: true,
    labelKey: 'editorQuickOpen',
  },
  {
    id: 'editorFindInFiles',
    keys: 'Ctrl+Shift+F',
    scope: 'editor',
    allowInInput: true,
    labelKey: 'editorFindInFiles',
  },
  {
    id: 'editorFormat',
    keys: 'Shift+Alt+F',
    scope: 'editor',
    allowInInput: true,
    labelKey: 'editorFormat',
  },
  // --- viewer (Step 17a) ---
  { id: 'viewerPrev', keys: 'ArrowLeft', scope: 'viewer', labelKey: 'viewerPrev' },
  { id: 'viewerNext', keys: 'ArrowRight', scope: 'viewer', labelKey: 'viewerNext' },
  { id: 'viewerZoomIn', keys: '+', alt: ['='], scope: 'viewer', labelKey: 'viewerZoomIn' },
  { id: 'viewerZoomOut', keys: '-', scope: 'viewer', labelKey: 'viewerZoomOut' },
  { id: 'viewerFit', keys: '0', scope: 'viewer', labelKey: 'viewerFit' },
  { id: 'viewerOriginal', keys: '1', scope: 'viewer', labelKey: 'viewerOriginal' },
  { id: 'viewerRotate', keys: 'R', scope: 'viewer', labelKey: 'viewerRotate' },
  { id: 'viewerRotateLeft', keys: 'Shift+R', scope: 'viewer', labelKey: 'viewerRotateLeft' },
  { id: 'viewerFullscreen', keys: 'F', scope: 'viewer', labelKey: 'viewerFullscreen' },
  { id: 'viewerInfo', keys: 'I', scope: 'viewer', labelKey: 'viewerInfo' },
  { id: 'viewerSlideshow', keys: 'Space', scope: 'viewer', labelKey: 'viewerSlideshow' },
  { id: 'viewerFind', keys: 'Ctrl+F', scope: 'viewer', labelKey: 'viewerFind' },
  // --- video player (Step 17b; scope pushed above `viewer`) ---
  { id: 'videoPlay', keys: 'Space', alt: ['K'], scope: 'video', labelKey: 'videoPlay' },
  { id: 'videoBack', keys: 'ArrowLeft', scope: 'video', labelKey: 'videoBack' },
  { id: 'videoForward', keys: 'ArrowRight', scope: 'video', labelKey: 'videoForward' },
  { id: 'videoBack10', keys: 'J', scope: 'video', labelKey: 'videoBack10' },
  { id: 'videoForward10', keys: 'L', scope: 'video', labelKey: 'videoForward10' },
  { id: 'videoFullscreen', keys: 'F', scope: 'video', labelKey: 'videoFullscreen' },
  { id: 'videoMute', keys: 'M', scope: 'video', labelKey: 'videoMute' },
  { id: 'videoVolumeUp', keys: 'ArrowUp', scope: 'video', labelKey: 'videoVolumeUp' },
  { id: 'videoVolumeDown', keys: 'ArrowDown', scope: 'video', labelKey: 'videoVolumeDown' },
  {
    id: 'videoPercent',
    keys: '0',
    alt: ['1', '2', '3', '4', '5', '6', '7', '8', '9'],
    scope: 'video',
    labelKey: 'videoPercent',
  },
]

export interface KeyCombo {
  key: string
  ctrl: boolean
  shift: boolean
  alt: boolean
}

const KEY_ALIASES: Record<string, string> = {
  esc: 'Escape',
  space: ' ',
  up: 'ArrowUp',
  down: 'ArrowDown',
  left: 'ArrowLeft',
  right: 'ArrowRight',
  del: 'Delete',
  '+': '+',
}

export function parseKeys(keys: string): KeyCombo {
  const parts = keys.split('+')
  // a trailing "+" key ("Ctrl++") appears as an empty last part
  const combo: KeyCombo = { key: '', ctrl: false, shift: false, alt: false }
  const named: string[] = []
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i]!
    if (p === '' && i === parts.length - 1) {
      named.push('+')
      continue
    }
    const l = p.toLowerCase()
    if (l === 'ctrl' || l === 'cmd' || l === 'meta') combo.ctrl = true
    else if (l === 'shift') combo.shift = true
    else if (l === 'alt' || l === 'option') combo.alt = true
    else named.push(p)
  }
  const raw = named[named.length - 1] ?? ''
  const alias = KEY_ALIASES[raw.toLowerCase()]
  combo.key = alias ?? (raw.length === 1 ? raw.toUpperCase() : raw)
  return combo
}

/** Normalise a KeyboardEvent key for comparison with `parseKeys`. */
export function eventKey(e: Pick<KeyboardEvent, 'key' | 'code'>): string {
  const k = e.key
  if (k.length === 1) return k === ' ' ? ' ' : k.toUpperCase()
  return k
}

export function matches(e: KeyboardEvent, combo: KeyCombo): boolean {
  const ctrl = e.ctrlKey || e.metaKey
  if (ctrl !== combo.ctrl || e.altKey !== combo.alt) return false
  // Shift is significant except for "?" (typed with Shift on most layouts) and "+".
  const shiftFree = combo.key === '?' || combo.key === '+'
  if (!shiftFree && e.shiftKey !== combo.shift) return false
  return eventKey(e) === combo.key
}

export function isEditableTarget(t: EventTarget | null): boolean {
  if (!(t instanceof HTMLElement)) return false
  const tag = t.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || t.isContentEditable
}

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)

/** Display parts for `<Kbd>` (`Ctrl` → `⌘` on macOS; arrows and Space as symbols). */
export function formatKeys(keys: string): string[] {
  return keys.split('+').map((p) => {
    switch (p) {
      case 'Ctrl':
        return isMac ? '⌘' : 'Ctrl'
      case 'Alt':
        return isMac ? '⌥' : 'Alt'
      case 'Shift':
        return isMac ? '⇧' : 'Shift'
      case 'ArrowLeft':
        return '←'
      case 'ArrowRight':
        return '→'
      case 'ArrowUp':
        return '↑'
      case 'ArrowDown':
        return '↓'
      case 'Space':
        return 'Space'
      case 'Escape':
        return 'Esc'
      case '':
        return '+'
      default:
        return p
    }
  })
}

export function shortcutById(id: string): ShortcutDef | undefined {
  return SHORTCUTS.find((s) => s.id === id)
}

// --- runtime registry ---

export type ShortcutHandler = (e: KeyboardEvent, keys: string) => void

interface Binding {
  def: ShortcutDef
  combos: { keys: string; combo: KeyCombo }[]
  handler: ShortcutHandler
}

const bindings = new Map<string, Binding>() // key: `${scope}:${id}`
const MODAL_SCOPES: ReadonlySet<ShortcutScope> = new Set(['dialog', 'viewer'])
const scopeStack: ShortcutScope[] = ['global']
let installed = false

function compile(def: ShortcutDef): Binding['combos'] {
  return [def.keys, ...(def.alt ?? [])].map((k) => ({ keys: k, combo: parseKeys(k) }))
}

function onKeyDown(e: KeyboardEvent): void {
  if (e.defaultPrevented) return
  const editable = isEditableTarget(e.target)
  // a modal scope (dialog, viewer) hides everything below it except global:
  // Delete/Backspace must not reach the file browser behind an overlay
  let from = 0
  for (let i = scopeStack.length - 1; i > 0; i--) {
    if (MODAL_SCOPES.has(scopeStack[i]!)) {
      from = i
      break
    }
  }
  const active: ShortcutScope[] = from > 0 ? ['global', ...scopeStack.slice(from)] : [...scopeStack]
  // the innermost scope wins: the video player (scope `video`, pushed above
  // `viewer`) owns ←/→/Space while the overlay's prev/next stay below it
  const seen = new Set<ShortcutScope>()
  for (let i = active.length - 1; i >= 0; i--) {
    const scope = active[i]!
    if (seen.has(scope)) continue
    seen.add(scope)
    for (const b of bindings.values()) {
      if (b.def.scope !== scope) continue
      if (editable && !b.def.allowInInput) continue
      for (const c of b.combos) {
        if (matches(e, c.combo)) {
          e.preventDefault()
          b.handler(e, c.keys)
          return
        }
      }
    }
  }
}

export function installShortcuts(): void {
  if (installed || typeof window === 'undefined') return
  installed = true
  window.addEventListener('keydown', onKeyDown)
}

/** Register a handler for a shortcut id; returns an unregister function. */
export function registerShortcut(id: string, handler: ShortcutHandler): () => void {
  const def = shortcutById(id)
  if (!def) throw new Error(`unknown shortcut ${id}`)
  installShortcuts()
  const key = `${def.scope}:${id}`
  bindings.set(key, { def, combos: compile(def), handler })
  return () => {
    if (bindings.get(key)?.handler === handler) bindings.delete(key)
  }
}

export function pushScope(scope: ShortcutScope): () => void {
  scopeStack.push(scope)
  return () => {
    const i = scopeStack.lastIndexOf(scope)
    if (i > 0) scopeStack.splice(i, 1)
  }
}

export function activeScopes(): readonly ShortcutScope[] {
  return scopeStack
}

/** Test helper. */
export function resetShortcuts(): void {
  bindings.clear()
  scopeStack.splice(1)
}

/** Duplicate key combinations inside one scope (SPEC §9.6: registry must be conflict-free). */
export function findConflicts(defs: ShortcutDef[] = SHORTCUTS): string[] {
  const seen = new Map<string, string>()
  const out: string[] = []
  for (const d of defs) {
    for (const k of [d.keys, ...(d.alt ?? [])]) {
      const c = parseKeys(k)
      const sig = `${d.scope}:${c.ctrl ? 'C' : ''}${c.shift ? 'S' : ''}${c.alt ? 'A' : ''}${c.key}`
      const prev = seen.get(sig)
      if (prev && prev !== d.id) out.push(`${d.scope}: ${k} used by ${prev} and ${d.id}`)
      seen.set(sig, d.id)
    }
  }
  return out
}
