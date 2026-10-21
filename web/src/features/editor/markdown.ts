/** Markdown rendering + sanitising (SPEC §8.13 "Markdown", §8.14 "Keamanan
 *  viewer"): marked (gfm) → DOMPurify strict profile — no script/style/
 *  iframe/object/embed/form, no event handlers, only http(s)/mailto/relative
 *  URLs. Pure functions (unit-tested); the preview component lives in
 *  MarkdownPreview.tsx. */
import DOMPurify from 'dompurify'
import { marked } from 'marked'

const SANITIZE = {
  USE_PROFILES: { html: true },
  FORBID_TAGS: [
    'style',
    'script',
    'iframe',
    'object',
    'embed',
    'form',
    'input',
    'button',
    'textarea',
    'select',
    'link',
    'meta',
    'base',
    'svg',
    'math',
    // review D: no inline media or image maps from documents either
    'audio',
    'video',
    'source',
    'track',
    'map',
    'area',
  ],
  FORBID_ATTR: ['style', 'srcset', 'formaction', 'xlink:href', 'usemap'],
  ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[^a-z0-9+.-]|[a-z0-9+.-]+(?:[^a-z0-9+.:-]|$))/i,
  ADD_ATTR: ['target'],
} as const

marked.setOptions({ gfm: true, breaks: false })

/** Pure renderer (unit-tested): HTML string safe to inject. */
export function renderMarkdown(source: string): string {
  const raw = marked.parse(source, { async: false }) as string
  return DOMPurify.sanitize(raw, SANITIZE as unknown as Parameters<typeof DOMPurify.sanitize>[1])
}

/** Resolve a relative markdown link/image against the document folder. */
export function resolveRelative(docPath: string, href: string): string | null {
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('//') || href.startsWith('#'))
    return null
  const dir = docPath.slice(0, docPath.lastIndexOf('/')) || '/'
  const base = href.startsWith('/') ? [] : dir.split('/').filter(Boolean)
  for (const seg of href.split('?')[0]!.split('#')[0]!.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      if (base.length === 0) return null
      base.pop()
      continue
    }
    base.push(seg)
  }
  return '/' + base.join('/')
}
