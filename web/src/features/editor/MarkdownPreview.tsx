import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { signUrl } from '@/lib/media-url'
import { useWorkspace } from './controller'
import { renderMarkdown, resolveRelative } from './markdown'
import { getModel } from './models'

/** Markdown preview pane (Step 16b): live render of the model with a rough
 *  scroll sync; relative images resolve to signed URLs, external ones become
 *  placeholders; relative links open in the editor. */

export function MarkdownPreview({ path }: { path: string }) {
  const { t } = useTranslation('editor')
  const ws = useWorkspace()
  const host = useRef<HTMLDivElement>(null)
  const model = getModel(path)
  const [source] = useSourceOf(model)
  const html = useMemo(() => renderMarkdown(source), [source])

  // images: relative → signed URL (same folder or below), external → placeholder
  useEffect(() => {
    const root = host.current
    if (!root) return
    let alive = true
    for (const img of Array.from(root.querySelectorAll('img'))) {
      const src = img.getAttribute('src') ?? ''
      const rel = resolveRelative(path, src)
      const swap = () => {
        const ph = document.createElement('span')
        ph.className =
          'inline-block rounded border border-border bg-surface-2 px-2 py-1 text-xs text-text-muted'
        ph.textContent = `${t('preview.imagePlaceholder')} (${img.getAttribute('alt') || src})`
        ph.dataset.placeholder = 'image'
        img.replaceWith(ph)
      }
      if (!rel) {
        swap()
        continue
      }
      img.removeAttribute('src')
      // a signed URL that does not resolve (file gone, not an image) also
      // ends as a placeholder
      img.addEventListener('error', () => alive && swap(), { once: true })
      signUrl(rel, 'media')
        .then((u) => {
          if (alive) img.src = u.url
        })
        .catch(() => alive && swap())
    }
    for (const a of Array.from(root.querySelectorAll('a[href]'))) {
      const href = a.getAttribute('href') ?? ''
      const rel = resolveRelative(path, href)
      if (rel) {
        a.setAttribute('title', t('preview.openLink', { name: rel.split('/').pop() }))
        a.addEventListener('click', (e) => {
          e.preventDefault()
          ws.openFile(rel)
        })
      } else {
        a.setAttribute('target', '_blank')
        a.setAttribute('rel', 'noopener noreferrer')
      }
    }
    return () => {
      alive = false
    }
  }, [html, path, t, ws])

  // rough scroll sync: editor scroll percentage → preview
  useEffect(() => {
    const ed = ws.editorRef.current
    const root = host.current
    if (!ed || !root) return
    const d = ed.onDidScrollChange((e) => {
      const max = e.scrollHeight - ed.getLayoutInfo().height
      if (max <= 0) return
      const pct = e.scrollTop / max
      root.scrollTop = pct * (root.scrollHeight - root.clientHeight)
    })
    return () => d.dispose()
  }, [ws, html])

  return (
    <div
      ref={host}
      className="markdown-preview h-full min-h-0 w-full overflow-auto border-l border-border bg-bg px-6 py-4 text-sm text-text"
      data-testid="markdown-preview"
      aria-label={t('preview.title')}
      // sanitised by DOMPurify in renderMarkdown
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

/** Live source of a model (debounced 150 ms). */
function useSourceOf(model: ReturnType<typeof getModel>) {
  const [src, setSrc] = useState(model?.getValue() ?? '')
  useEffect(() => {
    if (!model) return
    let timer: ReturnType<typeof setTimeout> | null = null
    const d = model.onDidChangeContent(() => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => setSrc(model.getValue()), 150)
    })
    return () => {
      d.dispose()
      if (timer) clearTimeout(timer)
    }
  }, [model, setSrc])
  return [src, setSrc] as const
}
