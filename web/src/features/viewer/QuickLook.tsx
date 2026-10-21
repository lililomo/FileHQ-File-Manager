import { ChevronLeft, ChevronRight, X } from 'lucide-react'
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { IconButton } from '@/components/ui/icon-button'
import type { Item } from '@/features/browser/api'
import { ItemFacts, ItemHeader } from '@/features/browser/ItemDetails'
import { useItemStat } from '@/features/browser/useItemStat'
import { useDownload } from '@/features/download/useDownload'
import { MetaControls } from '@/features/meta/MetaControls'
import { NoteCell } from '@/features/browser/NoteCell'
import type { TagColor } from '@/features/meta/api'
import { recordOpen } from '@/features/meta/api'
import { getMediaUrl } from '@/lib/media-url'
import { usePlayerStore } from '@/stores/player'
import { formatNumber } from '@/lib/format'
import { pushScope, registerShortcut } from '@/lib/shortcuts'
import { ImageViewer } from './image/ImageViewer'
import { InfoCard } from './InfoCard'
import { TextHead } from './text/TextHead'
import { viewerFor } from './viewers'

const PdfPages = lazy(() => import('./pdf/PdfPages').then((m) => ({ default: m.PdfPages })))

export interface QuickLookProps {
  /** the item Quick Look opens on (mount with `key` = its path) */
  item: Item
  /** the listing in display order: ←/→ move through it */
  items: Item[]
  onClose: () => void
  /** the item Quick Look moved to (the grid follows) */
  onMove?: ((item: Item) => void) | undefined
  onNoteSave?: ((path: string, note: string) => Promise<boolean>) | undefined
  onToggleFavorite?: ((item: Item) => void) | undefined
  onTogglePin?: ((item: Item) => void) | undefined
  onSetTag?: ((item: Item, color: TagColor | null) => void) | undefined
  /** restore focus to the grid after closing */
  restoreFocus?: (() => void) | undefined
}

/** Quick Look (Space; SPEC §8.14, PROMPT 17b task 5): a light 80 % modal over
 *  the browser with compact viewers — image, PDF pages 1–3, the first 200
 *  lines of text, a mini audio player, a muted video preview, a folder
 *  summary or the item card. Space/Esc close, ←/→ move through the folder. */
export function QuickLook({
  item: start,
  items,
  onClose,
  onMove,
  restoreFocus,
  ...meta
}: QuickLookProps) {
  const { t } = useTranslation(['viewer', 'files'])
  const [path, setPath] = useState(start.path)
  const index = useMemo(() => items.findIndex((it) => it.path === path), [items, path])
  const item = index >= 0 ? items[index]! : start
  const dl = useDownload()

  const move = (d: 1 | -1) => {
    if (index < 0) return
    const target = items[index + d]
    if (!target) return
    setPath(target.path)
    onMove?.(target)
  }
  // handlers through refs: the shortcuts and the scope live as long as the
  // dialog (the parent re-renders on every selection change)
  const moveRef = useRef(move)
  const closeRef = useRef(onClose)
  const focusRef = useRef(restoreFocus)
  useEffect(() => {
    moveRef.current = move
    closeRef.current = onClose
    focusRef.current = restoreFocus
  })
  useEffect(() => {
    const pop = pushScope('viewer')
    const offs = [
      registerShortcut('viewerSlideshow', () => closeRef.current()), // Space closes
      registerShortcut('viewerPrev', () => moveRef.current(-1)),
      registerShortcut('viewerNext', () => moveRef.current(1)),
    ]
    return () => {
      offs.forEach((off) => off())
      pop()
      const focus = focusRef.current
      if (focus) setTimeout(focus, 0)
    }
  }, [])
  useEffect(() => {
    recordOpen(item.path).catch(() => {})
  }, [item.path])

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        className="flex h-[80vh] w-[80vw] max-w-[80vw] flex-col gap-0 overflow-hidden p-0 sm:max-w-[80vw]"
        showCloseButton={false}
        onOpenAutoFocus={(e) => {
          e.preventDefault()
          ;(e.currentTarget as HTMLElement | null)?.focus()
        }}
        data-testid="quick-look"
        data-kind={item.isDir ? 'folder' : viewerFor(item)}
      >
        <DialogTitle className="sr-only">{item.name}</DialogTitle>
        <DialogDescription className="sr-only">{t('viewer:quick.description')}</DialogDescription>
        <header className="flex h-11 shrink-0 items-center gap-1 border-b border-border bg-surface px-2">
          <FileIcon
            category={item.isDir ? 'folder' : toIconCategory(item.category)}
            size={18}
            className="shrink-0"
          />
          <span
            className="min-w-0 flex-1 truncate text-sm font-medium"
            data-testid="quick-look-name"
          >
            {item.name}
          </span>
          <IconButton
            label={t('viewer:header.prev')}
            shortcut="←"
            disabled={index <= 0}
            onClick={() => move(-1)}
            data-testid="quick-look-prev"
          >
            <ChevronLeft aria-hidden="true" />
          </IconButton>
          <IconButton
            label={t('viewer:header.next')}
            shortcut="→"
            disabled={index < 0 || index >= items.length - 1}
            onClick={() => move(1)}
            data-testid="quick-look-next"
          >
            <ChevronRight aria-hidden="true" />
          </IconButton>
          <IconButton
            label={t('viewer:header.close')}
            shortcut="Space"
            onClick={onClose}
            data-testid="quick-look-close"
          >
            <X aria-hidden="true" />
          </IconButton>
        </header>
        <div className="min-h-0 flex-1">
          <ErrorBoundary
            resetKey={item.path}
            fallback={() => (
              <p className="p-6 text-center text-sm text-text-muted" role="alert">
                {t('viewer:renderError')}
              </p>
            )}
          >
            <CompactBody
              key={item.path}
              item={item}
              onDownload={() => void dl.downloadFile(item)}
              {...meta}
            />
          </ErrorBoundary>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function CompactBody({
  item,
  onDownload,
  onNoteSave,
  onToggleFavorite,
  onTogglePin,
  onSetTag,
}: {
  item: Item
  onDownload: () => void
} & Pick<QuickLookProps, 'onNoteSave' | 'onToggleFavorite' | 'onTogglePin' | 'onSetTag'>) {
  const { t } = useTranslation('viewer')
  const metaProps = { onToggleFavorite, onTogglePin, onSetTag, onNoteSave }
  if (item.isDir) return <FolderSummary item={item} metaProps={metaProps} />
  const kind = viewerFor(item)
  switch (kind) {
    case 'image':
      return (
        <ImageViewer
          item={item}
          index={-1}
          total={0}
          onGoTo={() => {}}
          nextItem={null}
          onDownload={onDownload}
          slideshow={false}
        />
      )
    case 'pdf':
      return (
        <Suspense
          fallback={<p className="p-6 text-center text-sm text-text-muted">{t('pdf.loading')}</p>}
        >
          <PdfPages path={item.path} pages={3} />
        </Suspense>
      )
    case 'text':
    case 'html':
    case 'markdown':
    case 'json':
    case 'table':
      return <TextHead path={item.path} etag={item.etag} lines={200} />
    case 'audio':
    case 'video':
      return <MediaPreview item={item} video={kind === 'video'} />
    default:
      return (
        <div className="flex h-full flex-col overflow-auto">
          <InfoCard item={item} kind={kind} onDownload={onDownload} />
          <div className="mx-auto w-full max-w-md px-6 pb-6">
            <MetaRow item={item} {...metaProps} />
          </div>
        </div>
      )
  }
}

/** Audio: a small native player; video: muted preview with poster. */
function MediaPreview({ item, video }: { item: Item; video: boolean }) {
  const [src, setSrc] = useState<string | null>(null)
  useEffect(() => {
    let alive = true
    getMediaUrl(item.path, 'media').then(
      (u) => alive && setSrc(u),
      () => {},
    )
    return () => {
      alive = false
    }
  }, [item.path])
  if (!src) return null
  return video ? (
    <div className="flex h-full items-center justify-center bg-black">
      <video
        src={src}
        poster={`${src}&variant=thumb`}
        muted
        autoPlay
        playsInline
        controls
        className="max-h-full max-w-full"
        data-testid="quick-look-video"
      />
    </div>
  ) : (
    <div className="flex h-full flex-col items-center justify-center gap-4 p-6">
      {item.thumb ? (
        <img src={`${src}&variant=thumb`} alt="" className="size-40 rounded-md object-cover" />
      ) : (
        <FileIcon category="audio" size={96} />
      )}
      <audio
        src={src}
        controls
        autoPlay
        // one sound at a time: the global player pauses while the preview plays
        onPlay={() => usePlayerStore.getState().setPlaying(false)}
        className="w-full max-w-md"
        data-testid="quick-look-audio"
      />
    </div>
  )
}

/** Folder: header, facts and the item count from `files/stat`. */
function FolderSummary({
  item,
  metaProps,
}: {
  item: Item
  metaProps: Pick<QuickLookProps, 'onToggleFavorite' | 'onTogglePin' | 'onSetTag' | 'onNoteSave'>
}) {
  const { t, i18n } = useTranslation('viewer')
  const lang = i18n.resolvedLanguage ?? 'id'
  const stat = useItemStat(item.path)
  const count = item.itemCount
  return (
    <div className="mx-auto max-w-md space-y-4 overflow-auto p-6" data-testid="quick-look-folder">
      <ItemHeader item={item} size={64} />
      <p className="text-sm text-text-muted">
        {count !== null
          ? t('quick.folderItems', { count, n: formatNumber(count, lang) })
          : t('quick.folderUnknown')}
      </p>
      <MetaRow item={item} {...metaProps} />
      <ItemFacts item={item} stat={stat.data?.stat} versionsCount={stat.data?.versionsCount} />
    </div>
  )
}

/** Favorite / pin / tag and the note (the card Quick Look showed before 17b). */
function MetaRow({
  item,
  onToggleFavorite,
  onTogglePin,
  onSetTag,
  onNoteSave,
}: { item: Item } & Pick<
  QuickLookProps,
  'onToggleFavorite' | 'onTogglePin' | 'onSetTag' | 'onNoteSave'
>) {
  const { t } = useTranslation('meta')
  return (
    <div className="space-y-3">
      <MetaControls
        item={item}
        onToggleFavorite={onToggleFavorite}
        onTogglePin={onTogglePin}
        onSetTag={onSetTag}
      />
      {onNoteSave ? (
        <div className="flex items-center gap-2 text-xs">
          <span className="text-text-muted">{t('note.title')}</span>
          <NoteCell item={item} onSave={onNoteSave} />
        </div>
      ) : null}
    </div>
  )
}
