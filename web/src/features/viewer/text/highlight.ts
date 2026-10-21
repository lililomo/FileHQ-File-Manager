/** Light syntax highlighting for the read-only text viewer (SPEC §8.14:
 *  highlight.js core, one lazy chunk per language — not Monaco). The output
 *  of highlight.js is escaped text inside `<span class="hljs-…">`; it is
 *  still passed through DOMPurify (only `span[class]`) before it reaches the
 *  DOM, and split into lines that each close/reopen their open spans so the
 *  viewer can virtualise rows. */
import DOMPurify from 'dompurify'
import type { HLJSApi, LanguageFn } from 'highlight.js'

type Loader = () => Promise<{ default: LanguageFn }>

/** Editor language id (`files/content.language`, `detectLanguage`) → loader. */
const LOADERS: Record<string, Loader> = {
  apache: () => import('highlight.js/lib/languages/apache'),
  bat: () => import('highlight.js/lib/languages/dos'),
  c: () => import('highlight.js/lib/languages/c'),
  cpp: () => import('highlight.js/lib/languages/cpp'),
  csharp: () => import('highlight.js/lib/languages/csharp'),
  css: () => import('highlight.js/lib/languages/css'),
  dart: () => import('highlight.js/lib/languages/dart'),
  dockerfile: () => import('highlight.js/lib/languages/dockerfile'),
  elixir: () => import('highlight.js/lib/languages/elixir'),
  go: () => import('highlight.js/lib/languages/go'),
  graphql: () => import('highlight.js/lib/languages/graphql'),
  handlebars: () => import('highlight.js/lib/languages/handlebars'),
  html: () => import('highlight.js/lib/languages/xml'),
  ini: () => import('highlight.js/lib/languages/ini'),
  java: () => import('highlight.js/lib/languages/java'),
  javascript: () => import('highlight.js/lib/languages/javascript'),
  json: () => import('highlight.js/lib/languages/json'),
  kotlin: () => import('highlight.js/lib/languages/kotlin'),
  less: () => import('highlight.js/lib/languages/less'),
  lua: () => import('highlight.js/lib/languages/lua'),
  makefile: () => import('highlight.js/lib/languages/makefile'),
  markdown: () => import('highlight.js/lib/languages/markdown'),
  nginx: () => import('highlight.js/lib/languages/nginx'),
  'objective-c': () => import('highlight.js/lib/languages/objectivec'),
  perl: () => import('highlight.js/lib/languages/perl'),
  php: () => import('highlight.js/lib/languages/php'),
  powershell: () => import('highlight.js/lib/languages/powershell'),
  protobuf: () => import('highlight.js/lib/languages/protobuf'),
  python: () => import('highlight.js/lib/languages/python'),
  r: () => import('highlight.js/lib/languages/r'),
  ruby: () => import('highlight.js/lib/languages/ruby'),
  rust: () => import('highlight.js/lib/languages/rust'),
  scala: () => import('highlight.js/lib/languages/scala'),
  scss: () => import('highlight.js/lib/languages/scss'),
  shell: () => import('highlight.js/lib/languages/bash'),
  sql: () => import('highlight.js/lib/languages/sql'),
  swift: () => import('highlight.js/lib/languages/swift'),
  typescript: () => import('highlight.js/lib/languages/typescript'),
  vb: () => import('highlight.js/lib/languages/vbnet'),
  xml: () => import('highlight.js/lib/languages/xml'),
  yaml: () => import('highlight.js/lib/languages/yaml'),
}

/** Above this size the viewer shows plain lines (highlighting would block). */
export const HIGHLIGHT_MAX_CHARS = 512 * 1024

let core: Promise<HLJSApi> | null = null

function hljsCore(): Promise<HLJSApi> {
  core ??= import('highlight.js/lib/core').then((m) => m.default)
  return core
}

export function canHighlight(language: string): boolean {
  return language in LOADERS
}

/** Highlighted HTML per line, or null when the language is unknown / too big. */
export async function highlightLines(text: string, language: string): Promise<string[] | null> {
  const load = LOADERS[language]
  if (!load || text.length > HIGHLIGHT_MAX_CHARS) return null
  const [hljs, lang] = await Promise.all([hljsCore(), load()])
  if (!hljs.getLanguage(language)) hljs.registerLanguage(language, lang.default)
  const html = hljs.highlight(text, { language, ignoreIllegals: true }).value
  const clean = DOMPurify.sanitize(html, { ALLOWED_TAGS: ['span'], ALLOWED_ATTR: ['class'] })
  return splitLines(clean)
}

const TAG = /<span class="([^"]*)">|<\/span>/g

/** Split highlighted HTML at newlines; spans open across a line break are
 *  closed at its end and reopened on the next line. */
export function splitLines(html: string): string[] {
  const out: string[] = []
  const open: string[] = []
  let line = ''
  let last = 0
  const flush = (chunk: string) => {
    const parts = chunk.split('\n')
    for (let i = 0; i < parts.length; i++) {
      line += parts[i]
      if (i < parts.length - 1) {
        out.push(line + '</span>'.repeat(open.length))
        line = open.map((c) => `<span class="${c}">`).join('')
      }
    }
  }
  for (const m of html.matchAll(TAG)) {
    flush(html.slice(last, m.index))
    if (m[1] !== undefined) open.push(m[1])
    else open.pop()
    line += m[0]
    last = m.index + m[0].length
  }
  flush(html.slice(last))
  out.push(line)
  return out
}
