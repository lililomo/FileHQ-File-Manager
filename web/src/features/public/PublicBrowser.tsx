import { useInfiniteQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ChevronRight, Download, Folder, Upload } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { Item, ListResponse } from '@/features/browser/api'
import { mergeItems } from '@/features/browser/useListing'
import { Thumb } from '@/features/browser/views/Thumb'
import { DropZone } from '@/features/upload/DropZone'
import { UploadInputs, type UploadInputsHandle } from '@/features/upload/UploadInputs'
import { uploadFromDataTransfer, uploadFromFileList, uploader } from '@/features/upload/useUploader'
import { ViewerOverlay } from '@/features/viewer/ViewerOverlay'
import { opensInViewer } from '@/features/viewer/viewers'
import { errorMessage } from '@/lib/errors'
import { formatBytes, formatDate } from '@/lib/format'
import { signDownloadUrl, startDownload } from '@/lib/media-url'
import { hasOsFiles } from '@/features/file-ops/dnd'
import { useTransfersStore } from '@/stores/transfers'
import { listPublic, publicListKey, publicZip, type PublicMeta, type PublicSortField } from './publicApi'

export interface PublicBrowserProps {
  token: string
  meta: PublicMeta
  /** folder inside the share ("/" = the shared folder) */
  path: string
  viewPath?: string | undefined
  onNavigate: (folder: string, view?: string) => void
}

const ROW = 40

/** Read-only browser of a shared folder (SPEC §8.15 "folder → browser
 *  read-only"): breadcrumb that starts at the shared folder, sort, a
 *  virtualised list with thumbnails, download per file / zip of everything,
 *  the viewer overlay for files, and file-request uploads. */
export function PublicBrowser({ token, meta, path, viewPath, onNavigate }: PublicBrowserProps) {
  const { t, i18n } = useTranslation('share')
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const [sort, setSort] = useState<PublicSortField>('name')
  const [dir, setDir] = useState<'asc' | 'desc'>('asc')
  const hidden = meta.hideListing
  const listing = useInfiniteQuery<ListResponse, Error, InfiniteData<ListResponse, string | null>, readonly unknown[], string | null>({
    queryKey: publicListKey(token, path, sort, dir),
    queryFn: ({ pageParam, signal }) => listPublic(token, path, sort, dir, pageParam, signal),
    initialPageParam: null,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
    enabled: !hidden,
    retry: false,
  })
  const items = useMemo(() => mergeItems(listing.data?.pages), [listing.data?.pages])
  const canDownload = meta.allowDownload && !meta.downloadLimitReached

  // --- breadcrumb inside the share -----------------------------------------
  const segments = path === '/' ? [] : path.split('/').filter(Boolean)
  const crumbs = [{ label: meta.name ?? t('public.root'), target: '/' }].concat(
    segments.map((seg, i) => ({ label: seg, target: '/' + segments.slice(0, i + 1).join('/') })),
  )

  // --- open: folders navigate, viewable files open the overlay, others download
  const open = useCallback(
    (it: Item) => {
      if (it.isDir) onNavigate(it.path)
      else if (opensInViewer(it)) onNavigate(path, it.path)
      else if (canDownload) void signDownloadUrl(it.path).then((u) => startDownload(u, it.name))
    },
    [onNavigate, path, canDownload],
  )

  const zipAll = async () => {
    try {
      const res = await publicZip(token, [path])
      startDownload(res.url, res.name)
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  // --- uploads (file request) ---------------------------------------------
  const inputs = useRef<UploadInputsHandle>(null)
  const container = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!meta.allowUpload) return
    return uploader.subscribe((ev) => {
      if (ev.type !== 'drained') return
      void qc.invalidateQueries({ queryKey: ['public', token, 'list'] })
      if (ev.done > 0) toast.success(t('public.uploadDone', { count: ev.done }))
      if (ev.errors > 0) toast.error(t('public.uploadFailed', { count: ev.errors }))
    })
  }, [meta.allowUpload, qc, token, t])
  useEffect(() => {
    const el = container.current
    if (!el || !meta.allowUpload) return
    const over = (e: DragEvent) => {
      if (hasOsFiles(e.dataTransfer)) e.preventDefault()
    }
    const drop = (e: DragEvent) => {
      if (!e.dataTransfer || !hasOsFiles(e.dataTransfer)) return
      e.preventDefault()
      void uploadFromDataTransfer(e.dataTransfer, '/')
    }
    el.addEventListener('dragover', over)
    el.addEventListener('drop', drop)
    return () => {
      el.removeEventListener('dragover', over)
      el.removeEventListener('drop', drop)
    }
  }, [meta.allowUpload])

  // --- virtual rows ---------------------------------------------------------
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
    if (last && last.index >= items.length - 20 && listing.hasNextPage && !listing.isFetchingNextPage)
      void listing.fetchNextPage()
  }, [virtualRows, items.length, listing])

  return (
    <div ref={container} className="relative flex flex-col gap-3" data-testid="public-browser">
      <nav aria-label={t('public.title')} className="flex flex-wrap items-center gap-1 text-sm">
        {crumbs.map((c, i) => (
          <span key={c.target} className="inline-flex items-center gap-1">
            {i > 0 ? <ChevronRight className="size-3.5 text-text-muted" aria-hidden="true" /> : null}
            {i === crumbs.length - 1 ? (
              <span className="font-medium" aria-current="page" data-testid="public-crumb-current">
                {c.label}
              </span>
            ) : (
              <button type="button" className="text-accent hover:underline" onClick={() => onNavigate(c.target)} data-testid="public-crumb">
                {c.label}
              </button>
            )}
          </span>
        ))}
      </nav>
      <div className="flex flex-wrap items-center gap-2">
        {!hidden ? (
          <>
            <Select value={sort} onValueChange={(v) => setSort(v as PublicSortField)}>
              <SelectTrigger className="h-8 w-32" aria-label={t('public.sortName')} data-testid="public-sort">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="name">{t('public.sortName')}</SelectItem>
                <SelectItem value="size">{t('public.sortSize')}</SelectItem>
                <SelectItem value="mtime">{t('public.sortMtime')}</SelectItem>
              </SelectContent>
            </Select>
            <Button variant="outline" size="sm" onClick={() => setDir((d) => (d === 'asc' ? 'desc' : 'asc'))} aria-pressed={dir === 'desc'}>
              {dir === 'asc' ? t('public.sortAsc') : t('public.sortDesc')}
            </Button>
            <span className="text-xs text-text-muted" data-testid="public-count">
              {t('public.items', { count: listing.data?.pages[0]?.total ?? items.length })}
            </span>
          </>
        ) : null}
        <span className="flex-1" />
        {meta.allowUpload ? (
          <>
            <UploadInputs ref={inputs} onFiles={(files) => void uploadFromFileList(files, '/')} testId="public-upload-input" />
            <Button variant="outline" size="sm" onClick={() => inputs.current?.pickFiles()} data-testid="public-upload">
              <Upload aria-hidden="true" />
              {t('public.upload')}
            </Button>
          </>
        ) : null}
        {canDownload && !hidden ? (
          <Button size="sm" onClick={() => void zipAll()} data-testid="public-zip">
            <Download aria-hidden="true" />
            {t('public.downloadAll')}
          </Button>
        ) : null}
      </div>
      {meta.allowUpload ? (
        <>
          <p className="text-xs text-text-muted">{t('public.uploadHint')}</p>
          <PublicUploads />
          <DropZone container={container} path={meta.name ?? '/'} />
        </>
      ) : null}
      {hidden ? (
        <EmptyState icon={Folder} title={t('public.hiddenListing')} className="rounded-lg border border-dashed" />
      ) : listing.isError ? (
        <EmptyState icon={Folder} title={t('public.error')} description={errorMessage(listing.error)} />
      ) : listing.isPending ? (
        <p className="text-sm text-text-muted">{t('public.loading')}</p>
      ) : items.length === 0 ? (
        <EmptyState icon={Folder} title={t('public.empty')} className="rounded-lg border border-dashed" />
      ) : (
        <div
          ref={scrollRef}
          className="max-h-[70dvh] overflow-auto rounded-lg border border-border"
          role="grid"
          aria-rowcount={items.length}
          data-testid="public-list"
        >
          <div className="sticky top-0 z-10 grid grid-cols-[minmax(0,1fr)_4.5rem_2.5rem] gap-2 border-b border-border bg-surface sm:grid-cols-[minmax(0,1fr)_6rem_10rem_2.5rem] px-2 py-1 text-xs font-medium text-text-muted" role="row">
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
              return (
                <div
                  key={it.path}
                  role="row"
                  data-path={it.path}
                  data-testid="public-row"
                  className="absolute left-0 grid w-full grid-cols-[minmax(0,1fr)_4.5rem_2.5rem] items-center sm:grid-cols-[minmax(0,1fr)_6rem_10rem_2.5rem] gap-2 border-b border-border/60 px-2 text-sm hover:bg-surface-2"
                  style={{ top: vr.start, height: ROW }}
                >
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
                    {!it.isDir && canDownload ? (
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={`${t('public.download')} ${it.name}`}
                        onClick={() =>
                          void signDownloadUrl(it.path).then((u) => startDownload(u, it.name))
                        }
                        data-testid="public-row-download"
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
        />
      ) : null}
    </div>
  )
}

/** Compact progress of the guest's uploads (the app's transfer panel links
 *  into the owner's browser, which guests cannot open). */
function PublicUploads() {
  const { t, i18n } = useTranslation('share')
  const lang = i18n.resolvedLanguage ?? 'id'
  const items = useTransfersStore((s) => s.items)
  const order = useTransfersStore((s) => s.order)
  const rows = order.map((id) => items[id]).filter((x): x is NonNullable<typeof x> => !!x)
  if (rows.length === 0) return null
  return (
    <ul className="space-y-1 text-xs" data-testid="public-uploads" aria-live="polite">
      {rows.slice(-5).map((tr) => (
        <li key={tr.id} className="flex items-center gap-2 rounded border border-border px-2 py-1" data-status={tr.status}>
          <span className="truncate">{tr.name}</span>
          <span className="ml-auto text-text-muted">
            {tr.status === 'done' || tr.status === 'skipped'
              ? t('public.uploadDone', { count: 1 })
              : tr.status === 'error'
                ? (tr.error?.message ?? t('public.uploadFailed', { count: 1 }))
                : `${formatBytes(tr.sent, lang)} / ${formatBytes(tr.size, lang)}`}
          </span>
        </li>
      ))}
    </ul>
  )
}
