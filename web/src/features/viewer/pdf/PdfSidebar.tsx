import type { PDFDocumentProxy } from 'pdfjs-dist'
import { ChevronRight } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'

export type OutlineItem = Awaited<ReturnType<PDFDocumentProxy['getOutline']>>[number]

export interface PdfSidebarProps {
  pdf: PDFDocumentProxy
  pageCount: number
  currentPage: number
  rotation: number
  onGoToPage: (n: number) => void
  outline: OutlineItem[] | null
  onGoToDest: (dest: OutlineItem['dest']) => void
}

const THUMB_WIDTH = 120

/** Thumbnails rendered lazily (IntersectionObserver) and kept per document;
 *  pdf.js' own PDFThumbnailViewer is not part of the components bundle. */
const thumbCache = new WeakMap<PDFDocumentProxy, Map<string, string>>()

function PageThumb({
  pdf,
  n,
  rotation,
  active,
  onClick,
}: {
  pdf: PDFDocumentProxy
  n: number
  rotation: number
  active: boolean
  onClick: () => void
}) {
  const { t } = useTranslation('viewer')
  const ref = useRef<HTMLButtonElement>(null)
  const key = `${n}:${rotation}`
  const [src, setSrc] = useState<string | null>(() => thumbCache.get(pdf)?.get(key) ?? null)
  useEffect(() => {
    const el = ref.current
    if (!el || src) return
    let cancelled = false
    const render = async () => {
      try {
        const page = await pdf.getPage(n)
        if (cancelled) return
        const base = page.getViewport({ scale: 1, rotation })
        const viewport = page.getViewport({ scale: THUMB_WIDTH / base.width, rotation })
        const canvas = document.createElement('canvas')
        canvas.width = Math.ceil(viewport.width)
        canvas.height = Math.ceil(viewport.height)
        await page.render({ canvas, viewport }).promise
        if (cancelled) return
        const url = canvas.toDataURL('image/jpeg', 0.7)
        let m = thumbCache.get(pdf)
        if (!m) {
          m = new Map()
          thumbCache.set(pdf, m)
        }
        m.set(key, url)
        setSrc(url)
      } catch {
        /* page failed to render: keep the placeholder */
      }
    }
    if (typeof IntersectionObserver === 'undefined') {
      void render()
      return
    }
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          io.disconnect()
          void render()
        }
      },
      { rootMargin: '300px' },
    )
    io.observe(el)
    return () => {
      cancelled = true
      io.disconnect()
    }
  }, [pdf, n, rotation, key, src])
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [active])
  return (
    <button
      ref={ref}
      type="button"
      onClick={onClick}
      aria-label={`${t('pdf.page')} ${n}`}
      aria-current={active ? 'page' : undefined}
      data-testid="pdf-thumb"
      data-page={n}
      className={cn(
        'flex w-full flex-col items-center gap-1 rounded-md border-2 p-1 outline-none',
        active
          ? 'border-accent'
          : 'border-transparent hover:border-border focus-visible:border-focus',
      )}
    >
      {src ? (
        <img src={src} alt="" width={THUMB_WIDTH} className="border border-border bg-white" />
      ) : (
        <div
          className="border border-border bg-surface-2"
          style={{ width: THUMB_WIDTH, height: Math.round(THUMB_WIDTH * 1.3) }}
          aria-hidden="true"
        />
      )}
      <span className="text-xs text-text-muted tabular">{n}</span>
    </button>
  )
}

function OutlineTree({
  items,
  depth,
  onGoToDest,
}: {
  items: OutlineItem[]
  depth: number
  onGoToDest: (dest: OutlineItem['dest']) => void
}) {
  return (
    <ul className={cn('space-y-0.5', depth > 0 && 'ml-3 border-l border-border pl-2')}>
      {items.map((it, i) => (
        <li key={`${depth}-${i}`}>
          <button
            type="button"
            onClick={() => onGoToDest(it.dest)}
            className="flex w-full items-start gap-1 rounded px-1 py-0.5 text-left text-xs hover:bg-surface-2 focus-visible:bg-surface-2"
            data-testid="pdf-outline-item"
          >
            {it.items.length > 0 ? (
              <ChevronRight className="mt-0.5 size-3 shrink-0 text-text-muted" aria-hidden="true" />
            ) : (
              <span className="w-3 shrink-0" aria-hidden="true" />
            )}
            <span className={cn('break-words', it.bold && 'font-semibold', it.italic && 'italic')}>
              {it.title}
            </span>
          </button>
          {it.items.length > 0 ? (
            <OutlineTree items={it.items} depth={depth + 1} onGoToDest={onGoToDest} />
          ) : null}
        </li>
      ))}
    </ul>
  )
}

/** Sidebar of the PDF viewer: page thumbnails and the document outline. */
export function PdfSidebar({
  pdf,
  pageCount,
  currentPage,
  rotation,
  onGoToPage,
  outline,
  onGoToDest,
}: PdfSidebarProps) {
  const { t } = useTranslation('viewer')
  const pages = Array.from({ length: pageCount }, (_, i) => i + 1)
  return (
    <aside
      className="flex w-44 shrink-0 flex-col border-r border-border bg-surface"
      aria-label={t('pdf.sidebar')}
      data-testid="pdf-sidebar"
    >
      <Tabs defaultValue="thumbs" className="flex min-h-0 flex-1 flex-col">
        <TabsList className="m-1 grid grid-cols-2">
          {/* review D: the 176 px sidebar clipped the long labels */}
          <TabsTrigger value="thumbs" className="min-w-0 truncate" title={t('pdf.thumbnailsLong')}>
            {t('pdf.thumbnails')}
          </TabsTrigger>
          <TabsTrigger value="outline" className="min-w-0 truncate" data-testid="pdf-tab-outline">
            {t('pdf.outline')}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="thumbs" className="min-h-0 flex-1 overflow-auto px-2 pb-2">
          <div className="space-y-1" data-testid="pdf-thumbs">
            {pages.map((n) => (
              <PageThumb
                key={n}
                pdf={pdf}
                n={n}
                rotation={rotation}
                active={n === currentPage}
                onClick={() => onGoToPage(n)}
              />
            ))}
          </div>
        </TabsContent>
        <TabsContent value="outline" className="min-h-0 flex-1 overflow-auto p-2">
          {outline === null ? null : outline.length === 0 ? (
            <p className="text-xs text-text-muted">{t('pdf.noOutline')}</p>
          ) : (
            <OutlineTree items={outline} depth={0} onGoToDest={onGoToDest} />
          )}
        </TabsContent>
      </Tabs>
    </aside>
  )
}
