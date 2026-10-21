import type { PDFDocumentLoadingTask, PDFDocumentProxy } from 'pdfjs-dist'
import type {
  EventBus,
  PDFFindController,
  PDFLinkService,
  PDFViewer as PdfjsPDFViewer,
} from 'pdfjs-dist/web/pdf_viewer.mjs'
import { Loader2 } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { Item } from '@/features/browser/api'
import { getMediaUrl } from '@/lib/media-url'
import { registerShortcut } from '@/lib/shortcuts'
import {
  loadPdfjs,
  nullL10n,
  PDF_ASSETS,
  PDF_DOC_OPTIONS,
  PRINT_MAX_PAGES,
  type Pdfjs,
} from './pdfjs'
import { PdfSearch, type FindUiState } from './PdfSearch'
import { PdfSidebar, type OutlineItem } from './PdfSidebar'
import { PdfToolbar } from './PdfToolbar'

export interface PdfViewerProps {
  item: Item
  onDownload: () => void
}

type Load =
  | { kind: 'loading' }
  | { kind: 'password'; wrong: boolean }
  | { kind: 'ready' }
  | { kind: 'error'; invalid: boolean }

interface Engine {
  pdfjs: Pdfjs
  eventBus: EventBus
  viewer: PdfjsPDFViewer
  linkService: PDFLinkService
  findController: PDFFindController
  task?: PDFDocumentLoadingTask | undefined
  passwordCb?: ((password: string) => void) | undefined
}

/** Outline entries scroll like pdf.js' `PDFLinkService.goToDestination`
 *  but without its follow-up `textLayer.div.focus()`: that focus lands once
 *  the destination page's text layer has rendered and would steal the focus
 *  from a find bar (Ctrl+F) opened in the meantime. */
async function scrollToDest(eng: Engine | null, dest: OutlineItem['dest']): Promise<void> {
  const doc = eng?.viewer.pdfDocument
  if (!eng || !doc || !dest) return
  try {
    const explicit = typeof dest === 'string' ? await doc.getDestination(dest) : dest
    if (!Array.isArray(explicit) || explicit.length === 0) return
    const ref: unknown = explicit[0]
    let pageNumber = 0
    if (typeof ref === 'number') pageNumber = ref + 1
    else if (ref && typeof ref === 'object')
      pageNumber = (await doc.getPageIndex(ref as { num: number; gen: number })) + 1
    if (pageNumber < 1 || pageNumber > doc.numPages) return
    eng.viewer.scrollPageIntoView({ pageNumber, destArray: explicit })
  } catch {
    /* unresolvable destination: stay where we are */
  }
}

// pdf.js setters live outside the component (React Compiler would otherwise
// flag writes to ref-derived objects)
function setViewerPage(v: PdfjsPDFViewer, n: number) {
  v.currentPageNumber = n
}
function setViewerScale(v: PdfjsPDFViewer, value: string) {
  v.currentScaleValue = value
}
function setViewerRotation(v: PdfjsPDFViewer, r: number) {
  v.pagesRotation = r
}

/** PDF viewer (SPEC §8.14 PdfViewer, PROMPT 17a task 5): pdf.js `PDFViewer`
 *  (virtualised pages, text layer for selection, read-only annotation layer
 *  with working internal links, find controller with highlights) fed through
 *  a signed URL with HTTP Range (64 KB chunks, no auto-fetch) so page 1 of a
 *  large file shows before the download completes. Password prompts,
 *  outline, thumbnails, rotation, zoom presets, print and download are
 *  built around it. Per-file state lives in the keyed inner component. */
export function PdfViewer(props: PdfViewerProps) {
  return <PdfDocument key={`${props.item.path}\u0000${props.item.etag}`} {...props} />
}

function PdfDocument({ item, onDownload }: PdfViewerProps) {
  const { t, i18n } = useTranslation('viewer')
  const lang = i18n.resolvedLanguage ?? 'id'
  const rootRef = useRef<HTMLDivElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const engine = useRef<Engine | null>(null)
  const [load, setLoad] = useState<Load>({ kind: 'loading' })
  const [pdf, setPdf] = useState<PDFDocumentProxy | null>(null)
  const [page, setPage] = useState(1)
  const [pageCount, setPageCount] = useState(0)
  const [scalePercent, setScalePercent] = useState(100)
  const [scaleValue, setScaleValue] = useState('auto')
  const [rotation, setRotation] = useState(0)
  const [sidebar, setSidebar] = useState(false)
  const [searchOpen, setSearchOpen] = useState(false)
  const [matches, setMatches] = useState<{ current: number; total: number } | null>(null)
  const [findState, setFindState] = useState<FindUiState>('idle')
  const [outline, setOutline] = useState<OutlineItem[] | null>(null)
  const [printing, setPrinting] = useState<number | null>(null)
  const [password, setPassword] = useState('')

  // --- engine + document ---
  useEffect(() => {
    let cancelled = false
    let eng: Engine | null = null
    const run = async () => {
      const pdfjs = await loadPdfjs()
      const container = containerRef.current
      if (cancelled || !container) return
      const { lib, viewer: v } = pdfjs
      const eventBus = new v.EventBus()
      const linkService = new v.PDFLinkService({
        eventBus,
        externalLinkTarget: v.LinkTarget.BLANK,
        externalLinkRel: 'noopener noreferrer nofollow',
      })
      const findController = new v.PDFFindController({ eventBus, linkService })
      const viewer = new v.PDFViewer({
        container,
        eventBus,
        linkService,
        findController,
        textLayerMode: 1, // ENABLE: selection/copy
        annotationMode: lib.AnnotationMode.ENABLE, // display + links, no forms/JS
        annotationEditorMode: -1, // AnnotationEditorType.DISABLE
        imageResourcesPath: `${PDF_ASSETS}images/`,
        l10n: nullL10n(lang) as never,
      })
      linkService.setViewer(viewer)
      eventBus.on('pagesinit', () => {
        viewer.currentScaleValue = 'auto'
      })
      eventBus.on('pagechanging', (e: { pageNumber: number }) => setPage(e.pageNumber))
      eventBus.on('scalechanging', (e: { scale: number; presetValue?: string }) => {
        setScalePercent(Math.round(e.scale * 100))
        setScaleValue(e.presetValue ?? String(e.scale))
      })
      const FindState = v.FindState as {
        FOUND: number
        NOT_FOUND: number
        WRAPPED: number
        PENDING: number
      }
      eventBus.on(
        'updatefindcontrolstate',
        (e: { state: number; matchesCount: { current: number; total: number } }) => {
          setMatches(e.matchesCount)
          setFindState(
            e.state === FindState.PENDING
              ? 'pending'
              : e.state === FindState.NOT_FOUND
                ? 'notfound'
                : e.state === FindState.WRAPPED
                  ? 'wrapped'
                  : 'found',
          )
        },
      )
      eventBus.on(
        'updatefindmatchescount',
        (e: { matchesCount: { current: number; total: number } }) => setMatches(e.matchesCount),
      )
      eng = { pdfjs, eventBus, viewer, linkService, findController }
      engine.current = eng

      // a 403 (expired signature) re-signs once; everything else is an error
      for (let attempt = 0; attempt < 2; attempt++) {
        const url = await getMediaUrl(item.path, 'media', { force: attempt > 0 })
        if (cancelled) return
        const task = lib.getDocument({ url, ...PDF_DOC_OPTIONS })
        task.onPassword = (cb: (pw: string) => void, reason: number) => {
          eng!.passwordCb = cb
          setLoad({ kind: 'password', wrong: reason === lib.PasswordResponses.INCORRECT_PASSWORD })
        }
        eng.task = task
        try {
          const doc = await task.promise
          if (cancelled) {
            void task.destroy()
            return
          }
          viewer.setDocument(doc)
          linkService.setDocument(doc)
          setPdf(doc)
          setPageCount(doc.numPages)
          setLoad({ kind: 'ready' })
          doc
            .getOutline()
            .then((o) => {
              if (!cancelled) setOutline(o ?? [])
            })
            .catch(() => {
              if (!cancelled) setOutline([])
            })
          return
        } catch (e) {
          if (cancelled) return
          const status = (e as { status?: number })?.status
          if (attempt === 0 && status === 403) continue
          setLoad({ kind: 'error', invalid: e instanceof lib.InvalidPDFException })
          return
        }
      }
    }
    void run()
    return () => {
      cancelled = true
      const cur = eng
      engine.current = null
      if (cur) {
        void cur.task?.destroy()
        try {
          cur.viewer.cleanup()
          cur.viewer.setDocument(null)
        } catch {
          /* viewer already gone */
        }
      }
    }
  }, [item.path, lang])

  // --- actions ---
  const goToPage = useCallback((n: number) => {
    const v = engine.current?.viewer
    if (v && n >= 1 && n <= v.pagesCount) setViewerPage(v, n)
  }, [])
  const zoomIn = useCallback(() => engine.current?.viewer.increaseScale(), [])
  const zoomOut = useCallback(() => engine.current?.viewer.decreaseScale(), [])
  const setScale = useCallback((v: string) => {
    const viewer = engine.current?.viewer
    if (viewer && v !== 'custom') setViewerScale(viewer, v)
  }, [])
  const rotate = useCallback(() => {
    const viewer = engine.current?.viewer
    if (!viewer) return
    const r = (viewer.pagesRotation + 90) % 360
    setViewerRotation(viewer, r)
    setRotation(r)
  }, [])
  const find = useCallback((query: string, type: '' | 'again', previous: boolean) => {
    const eng = engine.current
    if (!eng) return
    if (!query.trim()) {
      eng.eventBus.dispatch('findbarclose', { source: null })
      setMatches(null)
      setFindState('idle')
      return
    }
    eng.eventBus.dispatch('find', {
      source: null,
      type,
      query,
      caseSensitive: false,
      entireWord: false,
      highlightAll: true,
      findPrevious: previous,
      matchDiacritics: false,
    })
  }, [])
  const closeSearch = useCallback(() => {
    setSearchOpen(false)
    engine.current?.eventBus.dispatch('findbarclose', { source: null })
    setMatches(null)
    setFindState('idle')
    containerRef.current?.focus()
  }, [])
  const goToDest = useCallback((dest: OutlineItem['dest']) => {
    void scrollToDest(engine.current, dest)
  }, [])
  const toggleFullscreen = useCallback(() => {
    const el = rootRef.current
    if (!el) return
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => {})
    else void el.requestFullscreen?.().catch(() => {})
  }, [])
  const submitPassword = useCallback(() => {
    const cb = engine.current?.passwordCb
    if (!cb) return
    engine.current!.passwordCb = undefined
    setLoad({ kind: 'loading' })
    cb(password)
    setPassword('')
  }, [password])
  const cancelPassword = useCallback(() => {
    void engine.current?.task?.destroy()
    setLoad({ kind: 'error', invalid: false })
  }, [])

  // print: render ≤ 200 pages to images inside a print-only root (no iframe:
  // frame-src is 'none'), then window.print()
  const print = useCallback(async () => {
    const eng = engine.current
    if (!pdf || !eng || printing !== null) return
    const n = Math.min(pdf.numPages, PRINT_MAX_PAGES)
    if (pdf.numPages > PRINT_MAX_PAGES)
      toast.warning(t('pdf.printTooMany', { max: PRINT_MAX_PAGES }))
    setPrinting(0)
    const root = document.createElement('div')
    root.className = 'fm-pdf-print-root'
    const urls: string[] = []
    try {
      for (let i = 1; i <= n; i++) {
        const pg = await pdf.getPage(i)
        const viewport = pg.getViewport({ scale: 2, rotation: eng.viewer.pagesRotation })
        const canvas = document.createElement('canvas')
        canvas.width = Math.ceil(viewport.width)
        canvas.height = Math.ceil(viewport.height)
        await pg.render({ canvas, viewport, intent: 'print' }).promise
        const blob = await new Promise<Blob | null>((res) => canvas.toBlob(res, 'image/jpeg', 0.92))
        if (!blob) continue
        const url = URL.createObjectURL(blob)
        urls.push(url)
        const img = new Image()
        img.src = url
        root.appendChild(img)
        setPrinting(i)
      }
      document.body.appendChild(root)
      await Promise.all([...root.querySelectorAll('img')].map((im) => im.decode().catch(() => {})))
      const cleanup = () => {
        root.remove()
        urls.forEach((u) => URL.revokeObjectURL(u))
        window.removeEventListener('afterprint', cleanup)
      }
      window.addEventListener('afterprint', cleanup)
      window.print()
      // browsers without afterprint: tidy up after a while
      setTimeout(cleanup, 60_000)
    } finally {
      setPrinting(null)
    }
  }, [pdf, printing, t])

  // --- shortcuts (viewer scope; prev/next file belong to the overlay) ---
  useEffect(() => {
    const offs = [
      registerShortcut('viewerZoomIn', zoomIn),
      registerShortcut('viewerZoomOut', zoomOut),
      registerShortcut('viewerFit', () => setScale('page-fit')),
      registerShortcut('viewerOriginal', () => setScale('1')),
      registerShortcut('viewerRotate', rotate),
      registerShortcut('viewerFullscreen', toggleFullscreen),
      registerShortcut('viewerFind', () => setSearchOpen(true)),
    ]
    return () => offs.forEach((off) => off())
  }, [zoomIn, zoomOut, setScale, rotate, toggleFullscreen])

  // keyboard scrolling works once the container has focus
  useEffect(() => {
    if (load.kind === 'ready') containerRef.current?.focus()
  }, [load.kind])

  return (
    <div ref={rootRef} className="flex h-full min-h-0 flex-col bg-bg" data-testid="pdf-viewer">
      {load.kind === 'ready' ? (
        <PdfToolbar
          page={page}
          pageCount={pageCount}
          onPage={goToPage}
          scalePercent={scalePercent}
          scaleValue={scaleValue}
          onScaleValue={setScale}
          onZoomIn={zoomIn}
          onZoomOut={zoomOut}
          onRotate={rotate}
          sidebarOpen={sidebar}
          onToggleSidebar={() => setSidebar((s) => !s)}
          searchOpen={searchOpen}
          onToggleSearch={() => (searchOpen ? closeSearch() : setSearchOpen(true))}
          onPrint={() => void print()}
          printing={printing}
          onDownload={onDownload}
          onFullscreen={toggleFullscreen}
        />
      ) : null}
      {load.kind === 'ready' && searchOpen ? (
        <PdfSearch onFind={find} matches={matches} state={findState} onClose={closeSearch} />
      ) : null}
      <div className="flex min-h-0 flex-1">
        {load.kind === 'ready' && sidebar && pdf ? (
          <PdfSidebar
            pdf={pdf}
            pageCount={pageCount}
            currentPage={page}
            rotation={rotation}
            onGoToPage={goToPage}
            outline={outline}
            onGoToDest={goToDest}
          />
        ) : null}
        <div className="relative min-h-0 min-w-0 flex-1">
          {/* pdf.js needs an absolutely positioned scroll container with a .pdfViewer child */}
          <div
            ref={containerRef}
            className="fm-pdf-container outline-none"
            tabIndex={-1}
            aria-label={t('pdf.document')}
            data-testid="pdf-container"
            data-status={load.kind}
          >
            <div className="pdfViewer" />
          </div>
          {load.kind === 'loading' ? (
            <div
              className="absolute inset-0 flex items-center justify-center bg-surface-2 text-sm text-text-muted"
              aria-busy="true"
            >
              <Loader2 className="mr-2 size-4 animate-spin" aria-hidden="true" />
              {t('pdf.loading')}
            </div>
          ) : null}
          {load.kind === 'password' ? (
            <form
              className="absolute inset-0 flex items-center justify-center bg-surface-2"
              onSubmit={(e) => {
                e.preventDefault()
                submitPassword()
              }}
              data-testid="pdf-password"
            >
              <div className="w-80 space-y-3 rounded-lg border border-border bg-surface p-4">
                <p className="text-sm">{t('pdf.password')}</p>
                {load.wrong ? (
                  <p className="text-xs text-danger" role="alert" data-testid="pdf-password-wrong">
                    {t('pdf.passwordWrong')}
                  </p>
                ) : null}
                <div className="space-y-1">
                  <Label htmlFor="pdf-password-input">{t('pdf.passwordLabel')}</Label>
                  <Input
                    id="pdf-password-input"
                    type="password"
                    autoComplete="off"
                    autoFocus
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    data-testid="pdf-password-input"
                  />
                </div>
                <div className="flex justify-end gap-2">
                  <Button type="button" variant="outline" size="sm" onClick={cancelPassword}>
                    {t('pdf.passwordCancel')}
                  </Button>
                  <Button type="submit" variant="primary" size="sm" data-testid="pdf-password-open">
                    {t('pdf.passwordOpen')}
                  </Button>
                </div>
              </div>
            </form>
          ) : null}
          {load.kind === 'error' ? (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-surface-2 text-sm text-text-muted">
              <p role="alert">{load.invalid ? t('pdf.invalid') : t('pdf.error')}</p>
              <Button variant="outline" size="sm" onClick={onDownload}>
                {t('pdf.download')}
              </Button>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}
