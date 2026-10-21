import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getMediaUrl } from '@/lib/media-url'
import { loadPdfjs, PDF_DOC_OPTIONS } from './pdfjs'

/** Compact PDF preview (Quick Look, SPEC §8.14 / PROMPT 17b task 5): the
 *  first `pages` pages rendered to canvases at the box width, through the
 *  same Range-streaming options as the full viewer. Lazy chunk (pdf.js). */
export function PdfPages({ path, pages = 3 }: { path: string; pages?: number }) {
  const { t } = useTranslation('viewer')
  const host = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<'loading' | 'done' | 'error'>('loading')
  const [total, setTotal] = useState(0)

  useEffect(() => {
    let alive = true
    let destroy: (() => void) | null = null
    const box = host.current
    ;(async () => {
      const { lib } = await loadPdfjs()
      const url = await getMediaUrl(path, 'media')
      const task = lib.getDocument({ url, ...PDF_DOC_OPTIONS })
      destroy = () => void task.destroy()
      const doc = await task.promise
      if (!alive) return
      setTotal(doc.numPages)
      const root = box
      if (!root) return
      const width = Math.max(200, Math.min(root.clientWidth - 32, 900))
      for (let n = 1; n <= Math.min(pages, doc.numPages) && alive; n++) {
        const page = await doc.getPage(n)
        const base = page.getViewport({ scale: 1 })
        const viewport = page.getViewport({
          scale: (width / base.width) * (window.devicePixelRatio || 1),
        })
        const canvas = document.createElement('canvas')
        canvas.width = Math.floor(viewport.width)
        canvas.height = Math.floor(viewport.height)
        canvas.style.width = `${width}px`
        canvas.className = 'mx-auto mb-4 block bg-white shadow'
        canvas.setAttribute('aria-label', t('quick.pdfPage', { n }))
        canvas.dataset.testid = 'pdf-page-canvas'
        await page.render({ canvas, viewport }).promise
        if (alive) root.appendChild(canvas)
      }
      if (alive) setState('done')
    })().catch(() => alive && setState('error'))
    return () => {
      alive = false
      destroy?.()
      box?.replaceChildren()
    }
  }, [path, pages, t])

  return (
    <div className="h-full overflow-auto bg-surface-2 p-4" data-testid="pdf-pages">
      <div ref={host} />
      {state === 'loading' ? (
        <p className="text-center text-sm text-text-muted">{t('pdf.loading')}</p>
      ) : null}
      {state === 'error' ? (
        <p className="text-center text-sm text-text-muted">{t('pdf.error')}</p>
      ) : null}
      {state === 'done' && total > pages ? (
        <p className="text-center text-xs text-text-muted">
          {t('quick.pdfMore', { shown: pages, total })}
        </p>
      ) : null}
    </div>
  )
}
