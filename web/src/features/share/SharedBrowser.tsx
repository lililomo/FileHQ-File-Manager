import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useNavigate } from '@tanstack/react-router'
import {
  ChevronRight,
  Download,
  FilePlus2,
  Folder,
  FolderPlus,
  Pencil,
  Trash2,
  Upload,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
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
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  listDir,
  mkdir,
  statItem,
  type Item,
  type ListResponse,
  type SortField,
} from '@/features/browser/api'
import { mergeItems } from '@/features/browser/useListing'
import { Thumb } from '@/features/browser/views/Thumb'
import { createZip } from '@/features/download/api'
import { isEditableByName } from '@/features/editor/languages'
import { deleteItems, renameItem } from '@/features/file-ops/api'
import { hasOsFiles } from '@/features/file-ops/dnd'
import { DropZone } from '@/features/upload/DropZone'
import { UploadInputs, type UploadInputsHandle } from '@/features/upload/UploadInputs'
import { uploadFromDataTransfer, uploadFromFileList, uploader } from '@/features/upload/useUploader'
import { ViewerOverlay } from '@/features/viewer/ViewerOverlay'
import { opensInViewer } from '@/features/viewer/viewers'
import { errorMessage } from '@/lib/errors'
import { formatBytes, formatDate } from '@/lib/format'
import { signDownloadUrl, startDownload } from '@/lib/media-url'
import type { SharedHeader } from './sharedApi'

export interface SharedBrowserProps {
  header: SharedHeader
  path: string
  viewPath?: string | undefined
  onNavigate: (folder: string, view?: string) => void
}

const ROW = 40
type Sort = 'name' | 'size' | 'mtime'

const sharedListKey = (shareId: string, path: string, sort: Sort, dir: 'asc' | 'desc') =>
  ['shared-list', shareId, path, sort, dir] as const

/** Browser inside a received share (SPEC §8.15): breadcrumb from the shared
 *  item, sort, virtualised list with thumbnails, viewer overlay, download /
 *  zip, and — for editors — upload (drop or picker), new folder, rename,
 *  delete (into the owner's Recycle Bin) and "Edit" in the editor. Every
 *  call carries ?share= through the active share scope; paths are relative
 *  to the shared item. A single shared file shows one row. */
export function SharedBrowser({ header, path, viewPath, onNavigate }: SharedBrowserProps) {
  const { t, i18n } = useTranslation('share')
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const navigate = useNavigate()
  const editor = header.permission === 'editor'
  const canDownload = header.allowDownload || editor
  const [sort, setSort] = useState<Sort>('name')
  const [dir, setDir] = useState<'asc' | 'desc'>('asc')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [dialog, setDialog] = useState<
    { kind: 'mkdir' } | { kind: 'rename'; item: Item } | { kind: 'delete'; paths: string[] } | null
  >(null)
  const [name, setName] = useState('')

  // --- listing (folder share) or the single file (file share) --------------
  const listing = useInfiniteQuery<
    ListResponse,
    Error,
    InfiniteData<ListResponse, string | null>,
    readonly unknown[],
    string | null
  >({
    queryKey: sharedListKey(header.shareId, path, sort, dir),
    queryFn: ({ pageParam, signal }) =>
      listDir(
        path,
        { sort: sort as SortField, dir, foldersFirst: true, hidden: false },
        pageParam,
        signal,
      ),
    initialPageParam: null,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    enabled: header.isDir,
    retry: false,
  })
  const single = useInfiniteQuery<
    ListResponse,
    Error,
    InfiniteData<ListResponse, string | null>,
    readonly unknown[],
    string | null
  >({
    queryKey: ['shared-file', header.shareId],
    queryFn: async ({ signal }) => {
      const it = await statItem('/', signal)
      return {
        items: [it as unknown as Item],
        nextCursor: null,
        total: 1,
        path: '/',
        parentPath: null,
        dir: { path: '/', etag: '', mtime: '', itemCount: 1 },
      }
    },
    initialPageParam: null,
    getNextPageParam: () => undefined,
    enabled: !header.isDir,
    retry: false,
  })
  const source = header.isDir ? listing : single
  const items = useMemo(() => mergeItems(source.data?.pages), [source.data?.pages])
  const invalidate = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ['shared-list', header.shareId] })
  }, [qc, header.shareId])

  // --- breadcrumb inside the share ------------------------------------------
  const segments = path === '/' ? [] : path.split('/').filter(Boolean)
  const crumbs = [{ label: header.name, target: '/' }].concat(
    segments.map((seg, i) => ({ label: seg, target: '/' + segments.slice(0, i + 1).join('/') })),
  )

  // --- open -----------------------------------------------------------------
  const edit = useCallback(
    (it: Item) => {
      void navigate({ to: '/editor', search: { path: it.path, share: header.shareId } })
    },
    [navigate, header.shareId],
  )
  const download = async (it: Item) => {
    try {
      startDownload(await signDownloadUrl(it.path), it.name)
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }
  const open = useCallback(
    (it: Item) => {
      if (it.isDir) onNavigate(it.path)
      else if (opensInViewer(it)) onNavigate(path, it.path)
      else if (editor && isEditableByName(it.name, it.mime, it.category)) edit(it)
      else if (canDownload) void download(it)
    },
    [onNavigate, path, editor, canDownload, edit],
  )
  const zip = async (paths: string[]) => {
    try {
      const res = await createZip(paths)
      startDownload(res.url, res.name)
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  // --- editor operations ----------------------------------------------------
  const run = useMutation({
    mutationFn: async (): Promise<string> => {
      if (!dialog) return ''
      switch (dialog.kind) {
        case 'mkdir':
          await mkdir(path, name.trim())
          return t('shared.created')
        case 'rename':
          await renameItem(dialog.item.path, name.trim())
          return t('shared.renamed')
        case 'delete':
          await deleteItems(dialog.paths)
          return t('shared.deleted', { count: dialog.paths.length })
      }
    },
    onSuccess: (msg) => {
      if (msg) toast.success(msg)
      setDialog(null)
      setSelected(new Set())
      invalidate()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })

  // --- uploads (editor) -----------------------------------------------------
  const inputs = useRef<UploadInputsHandle>(null)
  const container = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!editor || !header.isDir) return
    return uploader.subscribe((ev) => {
      if (ev.type !== 'drained') return
      invalidate()
      if (ev.done > 0) toast.success(t('public.uploadDone', { count: ev.done }))
      if (ev.errors > 0) toast.error(t('public.uploadFailed', { count: ev.errors }))
    })
  }, [editor, header.isDir, invalidate, t])
  useEffect(() => {
    const el = container.current
    if (!el || !editor || !header.isDir) return
    const over = (e: DragEvent) => {
      if (hasOsFiles(e.dataTransfer)) e.preventDefault()
    }
    const drop = (e: DragEvent) => {
      if (!e.dataTransfer || !hasOsFiles(e.dataTransfer)) return
      e.preventDefault()
      void uploadFromDataTransfer(e.dataTransfer, path)
    }
    el.addEventListener('dragover', over)
    el.addEventListener('drop', drop)
    return () => {
      el.removeEventListener('dragover', over)
      el.removeEventListener('drop', drop)
    }
  }, [editor, header.isDir, path])

  // --- virtual rows -----------------------------------------------------------
  const scrollRef = useRef<HTMLDivElement>(null)
  const rowVirtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW,
    overscan: 10,
  })
  const virtualRows = rowVirtualizer.getVirtualItems()
  useEffect(() => {
    const last = virtualRows[virtualRows.length - 1]
    if (
      last &&
      last.index >= items.length - 20 &&
      listing.hasNextPage &&
      !listing.isFetchingNextPage
    )
      void listing.fetchNextPage()
  }, [virtualRows, items.length, listing])

  const toggle = (p: string) =>
    setSelected((s) => {
      const n = new Set(s)
      if (n.has(p)) n.delete(p)
      else n.add(p)
      return n
    })
  const selectedItems = items.filter((it) => selected.has(it.path))

  return (
    <div ref={container} className="relative flex flex-col gap-3" data-testid="shared-browser">
      {header.isDir ? (
        <nav aria-label={t('shared.title')} className="flex flex-wrap items-center gap-1 text-sm">
          {crumbs.map((c, i) => (
            <span key={c.target} className="inline-flex items-center gap-1">
              {i > 0 ? (
                <ChevronRight className="size-3.5 text-text-muted" aria-hidden="true" />
              ) : null}
              {i === crumbs.length - 1 ? (
                <span
                  className="font-medium"
                  aria-current="page"
                  data-testid="shared-crumb-current"
                >
                  {c.label}
                </span>
              ) : (
                <button
                  type="button"
                  className="text-accent hover:underline"
                  onClick={() => onNavigate(c.target)}
                  data-testid="shared-crumb"
                >
                  {c.label}
                </button>
              )}
            </span>
          ))}
        </nav>
      ) : null}
      <div className="flex flex-wrap items-center gap-2">
        {header.isDir ? (
          <>
            <Select value={sort} onValueChange={(v) => setSort(v as Sort)}>
              <SelectTrigger
                className="h-8 w-32"
                aria-label={t('public.sortName')}
                data-testid="shared-sort"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="name">{t('public.sortName')}</SelectItem>
                <SelectItem value="size">{t('public.sortSize')}</SelectItem>
                <SelectItem value="mtime">{t('public.sortMtime')}</SelectItem>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDir((d) => (d === 'asc' ? 'desc' : 'asc'))}
              aria-pressed={dir === 'desc'}
            >
              {dir === 'asc' ? t('public.sortAsc') : t('public.sortDesc')}
            </Button>
            <span className="text-xs text-text-muted" data-testid="shared-count">
              {t('public.items', { count: listing.data?.pages[0]?.total ?? items.length })}
            </span>
          </>
        ) : (
          <span className="text-xs text-text-muted">{t('shared.fileShare')}</span>
        )}
        <span className="flex-1" />
        {selected.size > 0 ? (
          <>
            <span className="text-xs text-text-muted" data-testid="shared-selected">
              {t('shared.selected', { count: selected.size })}
            </span>
            {canDownload ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => void zip([...selected])}
                data-testid="shared-zip-selected"
              >
                <Download aria-hidden="true" />
                {t('shared.downloadSelected', { count: selected.size })}
              </Button>
            ) : null}
            {editor ? (
              <Button
                variant="destructive"
                size="sm"
                onClick={() => setDialog({ kind: 'delete', paths: [...selected] })}
                data-testid="shared-delete"
              >
                <Trash2 aria-hidden="true" />
                {t('shared.delete')}
              </Button>
            ) : null}
            {editor && selectedItems.length === 1 ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setName(selectedItems[0]!.name)
                  setDialog({ kind: 'rename', item: selectedItems[0]! })
                }}
                data-testid="shared-rename"
              >
                <Pencil aria-hidden="true" />
                {t('shared.rename')}
              </Button>
            ) : null}
          </>
        ) : null}
        {editor && header.isDir ? (
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setName('')
                setDialog({ kind: 'mkdir' })
              }}
              data-testid="shared-mkdir"
            >
              <FolderPlus aria-hidden="true" />
              {t('shared.newFolder')}
            </Button>
            <UploadInputs
              ref={inputs}
              onFiles={(files) => void uploadFromFileList(files, path)}
              testId="shared-upload-input"
            />
            <Button
              variant="outline"
              size="sm"
              onClick={() => inputs.current?.pickFiles()}
              data-testid="shared-upload"
            >
              <Upload aria-hidden="true" />
              {t('shared.upload')}
            </Button>
          </>
        ) : null}
        {canDownload && header.isDir ? (
          <Button size="sm" onClick={() => void zip([path])} data-testid="shared-zip">
            <Download aria-hidden="true" />
            {t('public.downloadFolder')}
          </Button>
        ) : null}
      </div>
      {editor && header.isDir ? (
        <>
          <p className="text-xs text-text-muted">{t('shared.uploadHint')}</p>
          <DropZone container={container} path={header.name} />
        </>
      ) : null}
      {source.isError ? (
        <EmptyState
          icon={Folder}
          title={t('public.error')}
          description={errorMessage(source.error)}
        />
      ) : source.isPending ? (
        <p className="text-sm text-text-muted">{t('public.loading')}</p>
      ) : items.length === 0 ? (
        <EmptyState
          icon={Folder}
          title={t('public.empty')}
          className="rounded-lg border border-dashed"
        />
      ) : (
        <div
          ref={scrollRef}
          className="max-h-[70dvh] overflow-auto rounded-lg border border-border"
          role="grid"
          aria-rowcount={items.length}
          data-testid="shared-list"
        >
          <div
            className="sticky top-0 z-10 grid grid-cols-[2rem_minmax(0,1fr)_6rem_10rem_5rem] gap-2 border-b border-border bg-surface px-2 py-1 text-xs font-medium text-text-muted"
            role="row"
          >
            <span role="columnheader" />
            <span role="columnheader">{t('public.name')}</span>
            <span role="columnheader" className="text-right">
              {t('public.size')}
            </span>
            <span role="columnheader" className="hidden sm:block">
              {t('public.modified')}
            </span>
            <span role="columnheader" />
          </div>
          <div style={{ height: rowVirtualizer.getTotalSize(), position: 'relative' }}>
            {virtualRows.map((vr) => {
              const it = items[vr.index]!
              const editable =
                editor && !it.isDir && isEditableByName(it.name, it.mime, it.category)
              return (
                <div
                  key={it.path}
                  role="row"
                  data-path={it.path}
                  data-testid="shared-row"
                  aria-selected={selected.has(it.path)}
                  className="absolute left-0 grid w-full grid-cols-[2rem_minmax(0,1fr)_6rem_10rem_5rem] items-center gap-2 border-b border-border/60 px-2 text-sm hover:bg-surface-2 aria-selected:bg-surface-2"
                  style={{ top: vr.start, height: ROW }}
                >
                  <span role="gridcell" className="flex items-center">
                    <Checkbox
                      checked={selected.has(it.path)}
                      onCheckedChange={() => toggle(it.path)}
                      aria-label={t('shared.select', { name: it.name })}
                      data-testid="shared-check"
                    />
                  </span>
                  <button
                    type="button"
                    role="gridcell"
                    className="flex min-w-0 items-center gap-2 text-left"
                    onClick={() => open(it)}
                    onDoubleClick={() => open(it)}
                  >
                    {it.isDir ? (
                      <Folder className="size-5 shrink-0 text-accent" aria-hidden="true" />
                    ) : it.thumb ? (
                      <Thumb item={it} box={28} iconSize={20} className="shrink-0" />
                    ) : (
                      <FileIcon category={it.category} size={20} className="shrink-0" />
                    )}
                    <span className="truncate">{it.name}</span>
                  </button>
                  <span role="gridcell" className="text-right text-xs text-text-muted">
                    {it.isDir ? '' : formatBytes(it.size, lang)}
                  </span>
                  <span role="gridcell" className="hidden text-xs text-text-muted sm:block">
                    {formatDate(it.mtime, lang, { time: true })}
                  </span>
                  <span role="gridcell" className="flex justify-end">
                    {editable ? (
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={`${t('shared.edit')} ${it.name}`}
                        onClick={() => edit(it)}
                        data-testid="shared-row-edit"
                      >
                        <FilePlus2 aria-hidden="true" />
                      </Button>
                    ) : null}
                    {!it.isDir && canDownload ? (
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={`${t('public.download')} ${it.name}`}
                        onClick={() => void download(it)}
                        data-testid="shared-row-download"
                      >
                        <Download aria-hidden="true" />
                      </Button>
                    ) : null}
                  </span>
                </div>
              )
            })}
          </div>
        </div>
      )}
      {viewPath ? (
        <ViewerOverlay
          path={viewPath}
          items={items}
          paging={{
            hasNextPage: !!listing.hasNextPage,
            isFetchingNextPage: listing.isFetchingNextPage,
            fetchNextPage: () => void listing.fetchNextPage(),
          }}
          onClose={() => onNavigate(path)}
          onNavigate={(it) => onNavigate(path, it.path)}
          onOpenPath={(p) => onNavigate(path, p)}
          onEdit={editor ? edit : undefined}
        />
      ) : null}
      <Dialog
        open={dialog?.kind === 'mkdir' || dialog?.kind === 'rename'}
        onOpenChange={(o) => !o && setDialog(null)}
      >
        <DialogContent className="sm:max-w-sm" data-testid="shared-name-dialog">
          <DialogHeader>
            <DialogTitle>
              {dialog?.kind === 'rename' ? t('shared.rename') : t('shared.newFolder')}
            </DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              if (name.trim()) run.mutate()
            }}
            className="space-y-3"
          >
            <div className="space-y-1">
              <Label htmlFor="shared-name">
                {dialog?.kind === 'rename' ? t('shared.renameName') : t('shared.newFolderName')}
              </Label>
              <Input
                id="shared-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
                data-testid="shared-name"
              />
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setDialog(null)}>
                {t('shared.cancel')}
              </Button>
              <Button
                type="submit"
                disabled={run.isPending || !name.trim()}
                data-testid="shared-name-ok"
              >
                {t('shared.confirm')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <AlertDialog open={dialog?.kind === 'delete'} onOpenChange={(o) => !o && setDialog(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t('shared.deleteTitle', {
                count: dialog?.kind === 'delete' ? dialog.paths.length : 0,
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>{t('shared.deleteBody')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('shared.cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={() => run.mutate()} data-testid="shared-delete-confirm">
              {t('shared.delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
