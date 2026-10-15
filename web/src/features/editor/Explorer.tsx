import { useCallback, useEffect, useMemo, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { ChevronDown, ChevronRight, FolderPlus, FilePlus2, RefreshCw, Search } from 'lucide-react'
import { FileIcon } from '@/components/file-icon/FileIcon'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { listDir, listQueryKey, type Item } from '@/features/browser/api'
import { parentPath } from '@/features/browser/path'
import { useFileOps } from '@/features/file-ops/useFileOps'
import { NewItemDialog } from '@/features/file-ops/NewItemDialog'
import { cn } from '@/lib/utils'
import { useEditorStore } from '@/stores/editor'
import { useWorkspace } from './controller'

const LIST_OPTS = { limit: 500, foldersFirst: true as const }

export interface ExplorerProps {
  /** Path to expand and highlight (tab "Reveal in explorer"). */
  reveal: string | null
}

/** Lazy folder tree (SPEC §8.13 "Explorer"): `files/list` per expanded
 *  folder, client-side name filter with "search on server", context menu
 *  for new file/folder, rename, delete (Recycle Bin), refresh. */
export function Explorer({ reveal }: ExplorerProps) {
  const { t } = useTranslation('editor')
  const navigate = useNavigate()
  const [filter, setFilter] = useState('')
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(['/']))
  const [newItem, setNewItem] = useState<{ mode: 'file' | 'folder'; dir: string } | null>(null)
  const [rename, setRename] = useState<Item | null>(null)
  const [del, setDel] = useState<Item | null>(null)
  const qc = useQueryClient()
  const ws = useWorkspace()
  const activePath = useEditorStore((s) => s.activePath)

  // adjust the expanded set when a reveal request arrives (state from props, no effect)
  const [prevReveal, setPrevReveal] = useState<string | null>(null)
  if (reveal !== prevReveal) {
    setPrevReveal(reveal)
    if (reveal) {
      const next = new Set(expanded)
      let anc: string | null = parentPath(reveal)
      while (anc) {
        next.add(anc)
        anc = parentPath(anc)
      }
      next.add('/')
      setExpanded(next)
    }
  }
  useEffect(() => {
    if (!reveal) return
    const id = requestAnimationFrame(() => {
      document
        .querySelector<HTMLElement>(
          `[data-testid="explorer-node"][data-path="${CSS.escape(reveal)}"]`,
        )
        ?.scrollIntoView({ block: 'nearest' })
    })
    return () => cancelAnimationFrame(id)
  }, [reveal])

  const toggle = useCallback((path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }, [])
  const refresh = useCallback(
    (dir: string) => void qc.invalidateQueries({ queryKey: ['list', dir] }),
    [qc],
  )
  const needle = filter.trim().toLowerCase()
  return (
    <div
      className="flex h-full min-h-0 w-60 shrink-0 flex-col border-r border-border bg-surface-2"
      data-testid="editor-explorer"
    >
      <div className="flex h-8 items-center gap-1 px-2 text-xs font-medium uppercase tracking-wide text-text-muted">
        <span className="flex-1">{t('explorer.title')}</span>
        <IconButton
          size="icon-xs"
          label={t('explorer.newFile')}
          onClick={() => setNewItem({ mode: 'file', dir: '/' })}
        >
          <FilePlus2 />
        </IconButton>
        <IconButton
          size="icon-xs"
          label={t('explorer.newFolder')}
          onClick={() => setNewItem({ mode: 'folder', dir: '/' })}
        >
          <FolderPlus />
        </IconButton>
        <IconButton
          size="icon-xs"
          label={t('explorer.refresh')}
          onClick={() => void qc.invalidateQueries({ queryKey: ['list'] })}
        >
          <RefreshCw />
        </IconButton>
      </div>
      <div className="flex items-center gap-1 px-2 pb-1">
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={t('explorer.filter')}
          className="h-7 text-xs"
          aria-label={t('explorer.filter')}
          data-testid="explorer-filter"
        />
        {needle ? (
          <IconButton
            size="icon-xs"
            label={t('explorer.searchServer')}
            onClick={() =>
              void navigate({
                to: '/search',
                search: { q: filter.trim(), path: '/', kind: 'name' },
              })
            }
          >
            <Search />
          </IconButton>
        ) : null}
      </div>
      <div
        role="tree"
        aria-label={t('explorer.title')}
        className="min-h-0 flex-1 overflow-auto px-1 pb-2 text-sm"
      >
        <Node
          path="/"
          name={t('explorer.root')}
          isDir
          depth={0}
          expanded={expanded}
          toggle={toggle}
          needle={needle}
          activePath={activePath}
          onOpen={(p) => ws.openFile(p)}
          onNew={(mode, dir) => setNewItem({ mode, dir })}
          onRename={setRename}
          onDelete={setDel}
          onRefresh={refresh}
        />
      </div>
      {newItem ? (
        <NewItemDialog
          mode={newItem.mode}
          dir={newItem.dir}
          onClose={() => setNewItem(null)}
          onCreated={(it) => {
            setExpanded((prev) => new Set(prev).add(newItem.dir))
            if (!it.isDir) ws.openFile(it.path)
          }}
        />
      ) : null}
      <RenameDialog key={rename?.path ?? ''} item={rename} onClose={() => setRename(null)} />
      <DeleteConfirm item={del} onClose={() => setDel(null)} />
    </div>
  )
}

interface NodeProps {
  path: string
  name: string
  isDir: boolean
  item?: Item
  depth: number
  expanded: Set<string>
  toggle: (p: string) => void
  needle: string
  activePath: string | null
  onOpen: (p: string) => void
  onNew: (mode: 'file' | 'folder', dir: string) => void
  onRename: (it: Item) => void
  onDelete: (it: Item) => void
  onRefresh: (dir: string) => void
}

function Node(p: NodeProps) {
  const { t } = useTranslation('editor')
  const open = p.isDir && p.expanded.has(p.path)
  const q = useQuery({
    queryKey: listQueryKey(p.path, LIST_OPTS),
    queryFn: ({ signal }) => listDir(p.path, LIST_OPTS, null, signal),
    enabled: open,
    staleTime: 15_000,
  })
  const children = useMemo(() => {
    const items = q.data?.items ?? []
    if (!p.needle) return items
    return items.filter((it) => it.isDir || it.name.toLowerCase().includes(p.needle))
  }, [q.data, p.needle])
  const active = p.activePath === p.path
  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      if (p.isDir) p.toggle(p.path)
      else p.onOpen(p.path)
    } else if (e.key === 'ArrowRight' && p.isDir && !open) p.toggle(p.path)
    else if (e.key === 'ArrowLeft' && p.isDir && open) p.toggle(p.path)
  }
  const row = (
    <div
      role="treeitem"
      aria-expanded={p.isDir ? open : undefined}
      aria-selected={active}
      tabIndex={0}
      data-testid="explorer-node"
      data-path={p.path}
      title={p.path}
      onClick={() => (p.isDir ? p.toggle(p.path) : p.onOpen(p.path))}
      onKeyDown={onKey}
      style={{ paddingLeft: 4 + p.depth * 12 }}
      className={cn(
        'flex h-6 cursor-pointer items-center gap-1 rounded pr-1 outline-none select-none hover:bg-surface-3 focus-visible:ring-2 focus-visible:ring-focus',
        active && 'bg-accent/15 text-text',
      )}
    >
      {p.isDir ? (
        open ? (
          <ChevronDown className="size-3.5 shrink-0 text-text-muted" />
        ) : (
          <ChevronRight className="size-3.5 shrink-0 text-text-muted" />
        )
      ) : (
        <span className="w-3.5 shrink-0" />
      )}
      <FileIcon category={p.item?.category ?? 'folder'} size={14} className="shrink-0" />
      <span className="truncate">{p.name}</span>
    </div>
  )
  return (
    <>
      <ContextMenu>
        <ContextMenuTrigger asChild>{row}</ContextMenuTrigger>
        <ContextMenuContent>
          {p.isDir ? (
            <>
              <ContextMenuItem onSelect={() => p.onNew('file', p.path)}>
                {t('explorer.newFile')}
              </ContextMenuItem>
              <ContextMenuItem onSelect={() => p.onNew('folder', p.path)}>
                {t('explorer.newFolder')}
              </ContextMenuItem>
              <ContextMenuItem onSelect={() => p.onRefresh(p.path)}>
                {t('explorer.refresh')}
              </ContextMenuItem>
            </>
          ) : (
            <ContextMenuItem onSelect={() => p.onOpen(p.path)}>
              {t('explorer.open')}
            </ContextMenuItem>
          )}
          <ContextMenuItem
            onSelect={() => {
              void navigator.clipboard?.writeText(p.path)
              toast.message(t('toast.pathCopied'))
            }}
          >
            {t('explorer.copyPath')}
          </ContextMenuItem>
          {p.item ? (
            <>
              <ContextMenuSeparator />
              <ContextMenuItem onSelect={() => p.onRename(p.item!)}>
                {t('explorer.rename')}
              </ContextMenuItem>
              <ContextMenuItem onSelect={() => p.onDelete(p.item!)} className="text-destructive">
                {t('explorer.delete')}
              </ContextMenuItem>
            </>
          ) : null}
        </ContextMenuContent>
      </ContextMenu>
      {open ? (
        <div role="group">
          {q.isError ? (
            <div
              className="px-2 py-1 text-xs text-destructive"
              style={{ paddingLeft: 16 + p.depth * 12 }}
            >
              {t('explorer.loadError')}
            </div>
          ) : null}
          {q.isSuccess && children.length === 0 ? (
            <div
              className="px-2 py-1 text-xs text-text-muted"
              style={{ paddingLeft: 16 + p.depth * 12 }}
            >
              {t('explorer.empty')}
            </div>
          ) : null}
          {children.map((it) => (
            <Node
              key={it.path}
              path={it.path}
              name={it.name}
              isDir={it.isDir}
              item={it}
              depth={p.depth + 1}
              expanded={p.expanded}
              toggle={p.toggle}
              needle={p.needle}
              activePath={p.activePath}
              onOpen={p.onOpen}
              onNew={p.onNew}
              onRename={p.onRename}
              onDelete={p.onDelete}
              onRefresh={p.onRefresh}
            />
          ))}
        </div>
      ) : null}
    </>
  )
}

function RenameDialog({ item, onClose }: { item: Item | null; onClose: () => void }) {
  const { t } = useTranslation('editor')
  const ops = useFileOps()
  const ws = useWorkspace()
  const [name, setName] = useState(item?.name ?? '')
  const submit = async () => {
    if (!item || !name.trim() || name === item.name) return onClose()
    const res = await ops.rename(item.path, name.trim())
    if (res) {
      const to = (parentPath(item.path) === '/' ? '' : parentPath(item.path)) + '/' + name.trim()
      if (!item.isDir && ws.tab(item.path)) {
        ws.closeNow(item.path)
        ws.openFile(to)
      }
      onClose()
    }
  }
  return (
    <Dialog open={!!item} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{t('explorer.renameTitle')}</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
          className="space-y-3"
        >
          <Label htmlFor="ex-rename">{t('explorer.newName')}</Label>
          <Input id="ex-rename" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('dialog.cancel')}
            </Button>
            <Button type="submit">{t('explorer.rename')}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function DeleteConfirm({ item, onClose }: { item: Item | null; onClose: () => void }) {
  const { t } = useTranslation('editor')
  const ops = useFileOps()
  const ws = useWorkspace()
  return (
    <AlertDialog open={!!item} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t('explorer.deleteTitle', { name: item?.name ?? '' })}
          </AlertDialogTitle>
          <AlertDialogDescription>{t('explorer.deleteDesc')}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t('dialog.cancel')}</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              if (!item) return
              void ops.remove([item.path]).then((res) => {
                if (res) {
                  for (const tab of useEditorStore.getState().tabs) {
                    if (
                      tab.path === item.path ||
                      (item.isDir && tab.path.startsWith(item.path + '/'))
                    )
                      ws.closeNow(tab.path)
                  }
                }
              })
              onClose()
            }}
          >
            {t('explorer.delete')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
