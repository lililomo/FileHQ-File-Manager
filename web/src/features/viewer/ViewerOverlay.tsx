import { ChevronLeft, ChevronRight, Download, Info, Pencil, Share2, X } from 'lucide-react'
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import type { Item } from '@/features/browser/api'
import { useItemStat } from '@/features/browser/useItemStat'
import { useDownload } from '@/features/download/useDownload'
import { isEditableByName } from '@/features/editor/languages'
import { recordOpen } from '@/features/meta/api'
import { announce } from '@/lib/announce'
import { pushScope, registerShortcut } from '@/lib/shortcuts'
import { Filmstrip } from './image/Filmstrip'
import { ImageViewer } from './image/ImageViewer'
import { InfoCard } from './InfoCard'
import { InfoPanel } from './InfoPanel'
import { useSiblingNav, type SiblingNavPaging } from './useSiblingNav'

import { sameViewerKind, viewerFor } from './viewers'
import { usePlayerStore } from '@/stores/player'

// pdf.js, Video.js and the text viewers are lazy chunks (SPEC §8.14, §1.6)
const PdfViewer = lazy(() => import('./pdf/PdfViewer').then((m) => ({ default: m.PdfViewer })))
const VideoPlayer = lazy(() =>
  import('./video/VideoPlayer').then((m) => ({ default: m.VideoPlayer })),
)
const CodeViewer = lazy(() => import('./text/CodeViewer').then((m) => ({ default: m.CodeViewer })))
const MarkdownViewer = lazy(() =>
  import('./text/MarkdownViewer').then((m) => ({ default: m.MarkdownViewer })),
)
const CsvViewer = lazy(() => import('./text/CsvViewer').then((m) => ({ default: m.CsvViewer })))
const JsonViewer = lazy(() => import('./text/JsonTree').then((m) => ({ default: m.JsonViewer })))

export interface ViewerOverlayProps {
  /** virtual path of the file being viewed (`?view=`) */
  path: string
  /** loaded listing of the folder in display order (sibling navigation) */
  items: Item[]
  paging: SiblingNavPaging
  onClose: () => void
  /** prev/next: the caller replaces `?view=` */
  onNavigate: (item: Item) => void
  onEdit?: ((item: Item) => void) | undefined
  onOpenLocation?: ((item: Item) => void) | undefined
  /** a relative link inside a Markdown file (opens that file in the viewer) */
  onOpenPath?: ((path: string) => void) | undefined
  /** archives: open the archive browser (14b) */
  onBrowseArchive?: ((item: Item) => void) | undefined
  /** Called after the overlay unmounts: the opener (grid) takes focus back.
   *  The overlay is URL-driven, so Radix cannot always find the element that
   *  had focus when it opened. */
  restoreFocus?: (() => void) | undefined
}

/** Full-screen viewer shell (SPEC §8.14 "Kerangka overlay", PROMPT 17a task 1):
 *  header with name, "3 / 24", prev/next among files of the same kind,
 *  download, share (18a), info panel, edit (text), close; the body is the
 *  viewer picked from the registry, the info card otherwise. Radix Dialog
 *  provides the focus trap, `aria-modal` and focus return on close. */
export function ViewerOverlay({
  path,
  items,
  paging,
  onClose,
  onNavigate,
  onEdit,
  onOpenLocation,
  onOpenPath,
  onBrowseArchive,
  restoreFocus,
}: ViewerOverlayProps) {
  const { t } = useTranslation(['viewer', 'files'])
  const dl = useDownload()
  const [infoOpen, setInfoOpen] = useState(false)
  const contentRef = useRef<HTMLDivElement>(null)

  // the item: from the listing, else stat it (deep link beyond the loaded pages)
  const listed = useMemo(() => items.find((it) => it.path === path) ?? null, [items, path])
  const stat = useItemStat(listed ? null : path)
  const item: Item | null = listed ?? (stat.data as Item | undefined) ?? null
  const kind = item ? viewerFor(item) : 'info'
  const same = useCallback((it: Item) => !it.isDir && !!item && sameViewerKind(it, item), [item])
  const siblings = useSiblingNav(items, path, same, paging)
  const index = siblings.index
  const total = siblings.total
  const goTo = useCallback(
    (i: number) => {
      const target = siblings.list[i]
      if (target && target.path !== path) onNavigate(target)
    },
    [siblings.list, path, onNavigate],
  )
  const prev = siblings.prev
  const next = siblings.next

  // viewer scope: browser shortcuts (Delete, Backspace…) stay silent
  useEffect(() => pushScope('viewer'), [])
  // focus back to the opener once the dialog is gone (after Radix's own cleanup)
  const restoreRef = useRef(restoreFocus)
  useEffect(() => {
    restoreRef.current = restoreFocus
  }, [restoreFocus])
  useEffect(
    () => () => {
      const fn = restoreRef.current
      if (fn) setTimeout(fn, 0)
    },
    [],
  )
  useEffect(() => {
    const offs = [
      registerShortcut('viewerPrev', () => {
        if (prev) onNavigate(prev)
      }),
      registerShortcut('viewerNext', () => {
        if (next) onNavigate(next)
      }),
      registerShortcut('viewerInfo', () => setInfoOpen((o) => !o)),
    ]
    return () => offs.forEach((off) => off())
  }, [prev, next, onNavigate])

  // audio plays in the global player (SPEC §8.14: survives route changes):
  // the folder's audio files become the playlist, the overlay hands over to
  // the expanded player dialog
  const audioList = kind === 'audio' ? siblings.list : null
  const handedOver = useRef<string | null>(null)
  useEffect(() => {
    if (!item || kind !== 'audio' || !audioList || handedOver.current === item.path) return
    handedOver.current = item.path
    const list = audioList.length ? audioList : [item]
    const at = Math.max(
      0,
      list.findIndex((it) => it.path === item.path),
    )
    const st = usePlayerStore.getState()
    const cur = st.index >= 0 ? st.queue[st.index] : null
    if (cur?.path !== item.path) {
      st.playList(
        list.map((it) => ({
          path: it.path,
          name: it.name,
          mime: it.mime,
          ext: it.ext,
          size: it.size,
        })),
        at,
      )
    } else if (!st.playing) st.setPlaying(true)
    st.setExpanded(true)
    onClose()
  }, [item, kind, audioList, onClose])

  // Recent + announcement follow every file shown (SPEC §8.7)
  useEffect(() => {
    if (!item) return
    recordOpen(item.path).catch(() => {})
    announce(
      index >= 0
        ? `${item.name} — ${t('viewer:header.position', { index: index + 1, total })}`
        : item.name,
    )
  }, [item, index, total, t])

  const download = useCallback(() => {
    if (item) void dl.downloadFile(item)
  }, [dl, item])
  const editable = !!item && !!onEdit && isEditableByName(item.name, item.mime, item.category)

  let body: React.ReactNode
  if (!item) {
    body = stat.isError ? (
      <EmptyState
        title={t('files:error.notFound')}
        description={path}
        className="h-full"
        action={
          <Button variant="outline" onClick={onClose}>
            {t('viewer:header.close')}
          </Button>
        }
      />
    ) : (
      <div
        className="flex h-full items-center justify-center text-sm text-text-muted"
        aria-busy="true"
      >
        {t('viewer:image.loading')}
      </div>
    )
  } else if (kind === 'image') {
    body = (
      <ImageViewer
        item={item}
        index={index}
        total={total}
        onGoTo={goTo}
        nextItem={next}
        onDownload={download}
        slideshow
      />
    )
  } else {
    body = (
      <Suspense
        fallback={
          <div
            className="flex h-full items-center justify-center text-sm text-text-muted"
            aria-busy="true"
          >
            {kind === 'pdf' ? t('viewer:pdf.loading') : t('viewer:text.loading')}
          </div>
        }
      >
        {kind === 'pdf' ? (
          <PdfViewer item={item} onDownload={download} />
        ) : kind === 'video' ? (
          <VideoPlayer item={item} next={next} onNext={onNavigate} onDownload={download} />
        ) : kind === 'text' ? (
          <CodeViewer item={item} />
        ) : kind === 'html' ? (
          <CodeViewer item={item} note={t('viewer:text.htmlNote')} />
        ) : kind === 'markdown' ? (
          <MarkdownViewer item={item} onOpenPath={onOpenPath} />
        ) : kind === 'table' ? (
          <CsvViewer item={item} />
        ) : kind === 'json' ? (
          <JsonViewer item={item} />
        ) : kind === 'audio' ? (
          <div className="flex h-full items-center justify-center text-sm text-text-muted">
            {t('viewer:audio.starting')}
          </div>
        ) : (
          <InfoCard
            item={item}
            kind={kind}
            onDownload={download}
            onBrowseArchive={onBrowseArchive ? () => onBrowseArchive(item) : undefined}
          />
        )}
      </Suspense>
    )
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        className="inset-0 top-0 left-0 flex h-dvh w-screen max-w-none translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none border-0 p-0 sm:max-w-none"
        showCloseButton={false}
        onOpenAutoFocus={(e) => {
          e.preventDefault()
          contentRef.current?.focus()
        }}
        // Esc inside a sub-panel that handles it itself (PDF find bar) must
        // not close the whole viewer: Radix listens in the capture phase
        onEscapeKeyDown={(e) => {
          const target = e.target as HTMLElement | null
          if (target?.closest?.('[data-escape-local]')) e.preventDefault()
        }}
        data-testid="viewer-overlay"
        data-kind={kind}
      >
        <DialogTitle className="sr-only">{item?.name ?? t('viewer:title')}</DialogTitle>
        <DialogDescription className="sr-only">
          {t('viewer:dialogDescription', { name: item?.name ?? path })}
        </DialogDescription>
        <header className="flex h-12 shrink-0 items-center gap-1 border-b border-border bg-surface px-2">
          <div className="flex min-w-0 flex-1 items-center gap-2">
            {item ? (
              <FileIcon category={toIconCategory(item.category)} size={18} className="shrink-0" />
            ) : null}
            <span
              className="truncate text-sm font-medium"
              title={item?.name}
              data-testid="viewer-name"
            >
              {item?.name ?? ''}
            </span>
            {index >= 0 ? (
              <span
                className="shrink-0 text-xs text-text-muted tabular"
                data-testid="viewer-position"
              >
                {t('viewer:header.position', { index: index + 1, total })}
              </span>
            ) : null}
          </div>
          <div className="flex items-center gap-0.5">
            <IconButton
              label={t('viewer:header.prev')}
              shortcut="←"
              disabled={!prev}
              onClick={() => prev && onNavigate(prev)}
              data-testid="viewer-prev"
            >
              <ChevronLeft aria-hidden="true" />
            </IconButton>
            <IconButton
              label={t('viewer:header.next')}
              shortcut="→"
              disabled={!next}
              onClick={() => next && onNavigate(next)}
              data-testid="viewer-next"
            >
              <ChevronRight aria-hidden="true" />
            </IconButton>
          </div>
          <div className="flex items-center gap-0.5">
            <IconButton
              label={t('viewer:header.download')}
              onClick={download}
              disabled={!item}
              data-testid="viewer-download"
            >
              <Download aria-hidden="true" />
            </IconButton>
            <Tooltip>
              <TooltipTrigger asChild>
                <span tabIndex={-1} className="inline-flex">
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled
                    aria-label={t('viewer:header.share')}
                  >
                    <Share2 aria-hidden="true" />
                  </Button>
                </span>
              </TooltipTrigger>
              <TooltipContent>{t('viewer:header.shareSoon')}</TooltipContent>
            </Tooltip>
            {editable ? (
              <IconButton
                label={t('viewer:header.edit')}
                onClick={() => item && onEdit?.(item)}
                data-testid="viewer-edit"
              >
                <Pencil aria-hidden="true" />
              </IconButton>
            ) : null}
            <IconButton
              label={t('viewer:header.info')}
              shortcut="I"
              onClick={() => setInfoOpen((o) => !o)}
              aria-pressed={infoOpen}
              className={infoOpen ? 'bg-surface-2 text-text' : undefined}
              data-testid="viewer-info-toggle"
            >
              <Info aria-hidden="true" />
            </IconButton>
            <IconButton
              label={t('viewer:header.close')}
              shortcut="Esc"
              onClick={onClose}
              data-testid="viewer-close"
            >
              <X aria-hidden="true" />
            </IconButton>
          </div>
        </header>
        <div className="flex min-h-0 flex-1">
          <div
            ref={contentRef}
            tabIndex={-1}
            className="flex min-h-0 min-w-0 flex-1 flex-col outline-none"
          >
            <div className="min-h-0 flex-1">{body}</div>
            {kind === 'image' && siblings.list.length > 1 ? (
              <Filmstrip items={siblings.list} activeIndex={index} onSelect={onNavigate} />
            ) : null}
          </div>
          {infoOpen && item ? (
            <InfoPanel
              item={item}
              onClose={() => setInfoOpen(false)}
              onOpenLocation={onOpenLocation ? () => onOpenLocation(item) : undefined}
            />
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  )
}
