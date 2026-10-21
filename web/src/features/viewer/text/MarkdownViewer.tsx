import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Item } from '@/features/browser/api'
import { renderMarkdown, resolveRelative } from '@/features/editor/markdown'
import { signUrl } from '@/lib/media-url'
import { cn } from '@/lib/utils'
import { CodeViewer } from './CodeViewer'
import { BinaryFileError, loadText } from './source'
import { TextState } from './TextState'

/** Markdown viewer (SPEC §8.14): `marked` + DOMPurify exactly like the
 *  editor preview (16b) — relative images through signed URLs, external
 *  images as placeholders, relative links open the viewer — with a
 *  "Sumber" tab showing the highlighted source. */
export function MarkdownViewer({
  item,
  onOpenPath,
}: {
  item: Item
  onOpenPath?: ((path: string) => void) | undefined
}) {
  const { t } = useTranslation(['viewer', 'editor'])
  const [tab, setTab] = useState<'rendered' | 'source'>('rendered')
  const src = useQuery({
    queryKey: ['viewer-text', item.path, item.etag],
    queryFn: ({ signal }) => loadText(item, signal),
    staleTime: 30_000,
    retry: false,
  })
  const html = useMemo(() => (src.data ? renderMarkdown(src.data.text) : ''), [src.data])
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const root = host.current
    if (!root || tab !== 'rendered') return
    let alive = true
    for (const img of Array.from(root.querySelectorAll('img'))) {
      const raw = img.getAttribute('src') ?? ''
      const rel = resolveRelative(item.path, raw)
      const swap = () => {
        const ph = document.createElement('span')
        ph.className =
          'inline-block rounded border border-border bg-surface-2 px-2 py-1 text-xs text-text-muted'
        ph.textContent = `${t('editor:preview.imagePlaceholder')} (${img.getAttribute('alt') || raw})`
        ph.dataset.placeholder = 'image'
        img.replaceWith(ph)
      }
      if (!rel) {
        swap()
        continue
      }
      img.removeAttribute('src')
      img.addEventListener('error', () => alive && swap(), { once: true })
      signUrl(rel, 'media')
        .then((u) => {
          if (alive) img.src = u.url
        })
        .catch(() => alive && swap())
    }
    for (const a of Array.from(root.querySelectorAll('a[href]'))) {
      const href = a.getAttribute('href') ?? ''
      const rel = resolveRelative(item.path, href)
      if (rel && onOpenPath) {
        a.addEventListener('click', (e) => {
          e.preventDefault()
          onOpenPath(rel)
        })
      } else if (!href.startsWith('#')) {
        a.setAttribute('target', '_blank')
        a.setAttribute('rel', 'noopener noreferrer')
      }
    }
    return () => {
      alive = false
    }
  }, [html, item.path, onOpenPath, t, tab])

  if (src.isPending) return <TextState kind="loading" />
  if (src.isError)
    return <TextState kind={src.error instanceof BinaryFileError ? 'binary' : 'error'} />
  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="markdown-viewer">
      <div
        className="flex h-9 shrink-0 items-center gap-1 border-b border-border bg-surface px-2"
        role="tablist"
      >
        {(['rendered', 'source'] as const).map((k) => (
          <button
            key={k}
            type="button"
            role="tab"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={cn(
              'rounded-md px-2 py-1 text-xs',
              tab === k ? 'bg-surface-2 font-medium text-text' : 'text-text-muted hover:text-text',
            )}
            data-testid={`markdown-tab-${k}`}
          >
            {t(`viewer:markdown.${k}`)}
          </button>
        ))}
      </div>
      {tab === 'rendered' ? (
        <div
          ref={host}
          className="markdown-preview min-h-0 flex-1 overflow-auto bg-bg px-6 py-4 text-sm text-text"
          data-testid="markdown-rendered"
          // sanitised by DOMPurify in renderMarkdown (SPEC §8.14 profile)
          dangerouslySetInnerHTML={{ __html: html }}
        />
      ) : (
        <div className="min-h-0 flex-1">
          <CodeViewer item={item} />
        </div>
      )}
    </div>
  )
}
