import { useQueryClient } from '@tanstack/react-query'
import { ChevronRight, File as FileIconLucide, Folder, Loader2 } from 'lucide-react'
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'
import { listDir, listQueryKey, type Item } from '@/features/browser/api'
import { ROOT, splitPath } from '@/features/browser/path'
import { cn } from '@/lib/utils'

export interface FolderPickerProps {
  value: string | null
  onChange: (path: string) => void
  /** `folders` (default) lists folders only; `all` also lists files (symlink target). */
  mode?: 'folders' | 'all'
  /** Paths that cannot be chosen (e.g. the items being moved and their children). */
  isDisabled?: (path: string, item: Item | null) => boolean
  className?: string
  autoFocus?: boolean
  /** Activate on double-click / Enter of a selectable node. */
  onActivate?: (path: string) => void
}

export interface FolderPickerHandle {
  /** Re-fetch the children of a folder (after creating one) and expand it. */
  reload: (path: string) => Promise<void>
}

type Children = Item[] | 'loading' | 'error'

interface Node {
  path: string
  name: string
  depth: number
  item: Item | null
  isDir: boolean
  expanded: boolean
  children: Children | undefined
}

/** Lazy folder tree (SPEC §8.5 "Dialog Pindah ke…"): role=tree with
 *  aria-activedescendant, arrow keys, Enter selects. Children are fetched
 *  with `files/list?types=folder` when a node is expanded. */
export const FolderPicker = forwardRef<FolderPickerHandle, FolderPickerProps>(function FolderPicker(
  { value, onChange, mode = 'folders', isDisabled, className, autoFocus, onActivate },
  ref,
) {
  const { t } = useTranslation('fileops')
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set([ROOT]))
  const [children, setChildren] = useState<Map<string, Children>>(new Map())
  const [active, setActive] = useState<string>(value ?? ROOT)
  const id = useRef(`picker-${Math.random().toString(36).slice(2, 8)}`).current
  const treeRef = useRef<HTMLDivElement>(null)

  const load = useCallback(
    async (path: string, force = false) => {
      if (!force && children.get(path) !== undefined && children.get(path) !== 'error') return
      setChildren((m) => new Map(m).set(path, 'loading'))
      try {
        const opts = mode === 'folders' ? { types: 'folder', limit: 500 } : { limit: 500 }
        const res = await qc.fetchQuery({
          queryKey: [...listQueryKey(path, opts), 'picker'],
          queryFn: ({ signal }) => listDir(path, opts, null, signal),
          staleTime: force ? 0 : 15_000,
        })
        setChildren((m) => new Map(m).set(path, res.items))
      } catch {
        setChildren((m) => new Map(m).set(path, 'error'))
      }
    },
    [children, mode, qc],
  )

  // expand the root and the ancestors of the initial value
  const initial = useRef(value)
  useEffect(() => {
    const parts = splitPath(initial.current ?? ROOT)
    const dirs = [ROOT]
    for (let i = 1; i < parts.length; i++) dirs.push(`/${parts.slice(0, i).join('/')}`)
    if (initial.current && initial.current !== ROOT && mode === 'folders') {
      // the value itself is a folder: show its children too
      dirs.push(initial.current)
    }
    setExpanded(new Set(dirs))
    for (const d of dirs) void load(d)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useImperativeHandle(ref, () => ({
    reload: async (path) => {
      setExpanded((s) => new Set(s).add(path))
      await load(path, true)
    },
  }))

  const nodes = useMemo(() => {
    const out: Node[] = []
    const walk = (path: string, name: string, depth: number, item: Item | null) => {
      const isDir = item ? item.isDir : true
      const exp = isDir && expanded.has(path)
      const kids = isDir ? children.get(path) : undefined
      out.push({ path, name, depth, item, isDir, expanded: exp, children: kids })
      if (exp && Array.isArray(kids)) {
        for (const k of kids) walk(k.path, k.name, depth + 1, k)
      }
    }
    walk(ROOT, t('picker.root'), 0, null)
    return out
  }, [expanded, children, t])

  const toggle = (n: Node) => {
    if (!n.isDir) return
    setExpanded((s) => {
      const next = new Set(s)
      if (next.has(n.path)) next.delete(n.path)
      else next.add(n.path)
      return next
    })
    if (!expanded.has(n.path)) void load(n.path)
  }
  const select = (n: Node) => {
    setActive(n.path)
    if (isDisabled?.(n.path, n.item)) return
    if (mode === 'folders' && !n.isDir) return
    onChange(n.path)
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const i = Math.max(
      0,
      nodes.findIndex((n) => n.path === active),
    )
    const cur = nodes[i]
    if (!cur) return
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        if (nodes[i + 1]) select(nodes[i + 1]!)
        return
      case 'ArrowUp':
        e.preventDefault()
        if (nodes[i - 1]) select(nodes[i - 1]!)
        return
      case 'ArrowRight':
        e.preventDefault()
        if (cur.isDir && !cur.expanded) toggle(cur)
        else if (nodes[i + 1] && nodes[i + 1]!.depth > cur.depth) select(nodes[i + 1]!)
        return
      case 'ArrowLeft': {
        e.preventDefault()
        if (cur.isDir && cur.expanded) {
          toggle(cur)
          return
        }
        for (let k = i - 1; k >= 0; k--) {
          if (nodes[k]!.depth < cur.depth) {
            select(nodes[k]!)
            return
          }
        }
        return
      }
      case 'Home':
        e.preventDefault()
        select(nodes[0]!)
        return
      case 'End':
        e.preventDefault()
        select(nodes[nodes.length - 1]!)
        return
      case 'Enter':
        e.preventDefault()
        select(cur)
        onActivate?.(cur.path)
        return
      case ' ':
        e.preventDefault()
        toggle(cur)
        return
      default:
    }
  }

  // keep the active row visible
  useEffect(() => {
    const el = treeRef.current?.querySelector<HTMLElement>(`[data-path="${CSS.escape(active)}"]`)
    el?.scrollIntoView({ block: 'nearest' })
  }, [active, nodes.length])

  return (
    <div
      ref={treeRef}
      role="tree"
      aria-label={t('picker.label')}
      aria-activedescendant={`${id}-${nodes.findIndex((n) => n.path === active)}`}
      tabIndex={0}
      autoFocus={autoFocus}
      onKeyDown={onKeyDown}
      className={cn(
        'max-h-64 overflow-auto rounded-md border border-border bg-bg p-1 text-sm outline-none focus-visible:ring-2 focus-visible:ring-focus',
        className,
      )}
      data-testid="folder-picker"
    >
      {nodes.map((n, i) => {
        const disabled = !!isDisabled?.(n.path, n.item) || (mode === 'folders' && !n.isDir)
        const selected = value === n.path
        const isActive = active === n.path
        return (
          <div
            key={n.path}
            id={`${id}-${i}`}
            role="treeitem"
            aria-level={n.depth + 1}
            aria-expanded={n.isDir ? n.expanded : undefined}
            aria-selected={selected}
            aria-disabled={disabled || undefined}
            data-path={n.path}
            data-testid={`picker-node-${n.path}`}
            className={cn(
              'flex h-7 cursor-default items-center gap-1 rounded px-1 select-none',
              selected && 'bg-accent-subtle text-text',
              !selected && 'hover:bg-surface-2',
              isActive && 'outline outline-1 -outline-offset-1 outline-accent/60',
              disabled && 'opacity-50',
            )}
            style={{ paddingLeft: 4 + n.depth * 16 }}
            onClick={() => select(n)}
            onDoubleClick={() => {
              if (n.isDir) toggle(n)
              if (!disabled) onActivate?.(n.path)
            }}
          >
            <button
              type="button"
              tabIndex={-1}
              aria-label={n.expanded ? t('picker.collapse') : t('picker.expand')}
              className={cn(
                'grid size-5 shrink-0 place-items-center rounded text-text-muted',
                !n.isDir && 'invisible',
              )}
              onClick={(e) => {
                e.stopPropagation()
                toggle(n)
              }}
            >
              {n.children === 'loading' ? (
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              ) : (
                <ChevronRight
                  className={cn('size-3.5 transition-transform', n.expanded && 'rotate-90')}
                  aria-hidden="true"
                />
              )}
            </button>
            {n.isDir ? (
              <Folder className="size-4 shrink-0 text-icon-folder" aria-hidden="true" />
            ) : (
              <FileIconLucide className="size-4 shrink-0 text-text-muted" aria-hidden="true" />
            )}
            <span className="truncate">{n.name}</span>
            {n.children === 'error' ? (
              <span className="ml-auto text-xs text-danger">{t('picker.error')}</span>
            ) : null}
          </div>
        )
      })}
    </div>
  )
})
