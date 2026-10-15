/** Lazy Monaco loader (ADR-05): the editor chunk and its workers are
 *  imported only when a workspace mounts; workers come from the bundle
 *  (Vite `?worker`, same origin — CSP `worker-src 'self'`), never a CDN.
 *  Custom Monarch grammars (nginx, apache, makefile) are registered once. */
import type * as Monaco from 'monaco-editor'
import { apacheConfiguration, apacheLanguage } from './monarch/apache'
import { makefileConfiguration, makefileLanguage } from './monarch/makefile'
import { nginxConfiguration, nginxLanguage } from './monarch/nginx'
import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker'
import JsonWorker from 'monaco-editor/language/json/json.worker.js?worker'
import CssWorker from 'monaco-editor/language/css/css.worker.js?worker'
import HtmlWorker from 'monaco-editor/language/html/html.worker.js?worker'
import TsWorker from 'monaco-editor/language/typescript/ts.worker.js?worker'

export type MonacoApi = typeof Monaco

let loading: Promise<MonacoApi> | null = null

type WorkerCtor = new () => Worker

/** Worker per language service; Monaco asks for one lazily per language. */
const WORKERS: Record<string, WorkerCtor> = {
  editor: EditorWorker,
  json: JsonWorker,
  css: CssWorker,
  scss: CssWorker,
  less: CssWorker,
  html: HtmlWorker,
  handlebars: HtmlWorker,
  razor: HtmlWorker,
  typescript: TsWorker,
  javascript: TsWorker,
}

export function loadMonaco(): Promise<MonacoApi> {
  if (loading) return loading
  loading = (async () => {
    const monaco = await import('monaco-editor')
    const env =
      (self as unknown as { MonacoEnvironment?: Monaco.Environment }).MonacoEnvironment ?? {}
    env.getWorker = (_id: string, label: string) => new (WORKERS[label] ?? EditorWorker)()
    ;(self as unknown as { MonacoEnvironment?: Monaco.Environment }).MonacoEnvironment = env
    registerLanguages(monaco)
    return monaco
  })()
  return loading
}

function registerLanguages(monaco: MonacoApi): void {
  const have = new Set(monaco.languages.getLanguages().map((l) => l.id))
  const custom: [
    string,
    Monaco.languages.IMonarchLanguage,
    Monaco.languages.LanguageConfiguration,
    string[],
    string[],
  ][] = [
    ['nginx', nginxLanguage, nginxConfiguration, ['.nginx'], ['nginx.conf', 'mime.types']],
    [
      'apache',
      apacheLanguage,
      apacheConfiguration,
      ['.htaccess'],
      ['.htaccess', 'httpd.conf', 'apache2.conf'],
    ],
    [
      'makefile',
      makefileLanguage,
      makefileConfiguration,
      ['.mk'],
      ['Makefile', 'makefile', 'GNUmakefile'],
    ],
  ]
  for (const [id, lang, conf, extensions, filenames] of custom) {
    if (have.has(id)) continue
    monaco.languages.register({ id, extensions, filenames })
    monaco.languages.setMonarchTokensProvider(id, lang)
    monaco.languages.setLanguageConfiguration(id, conf)
  }
}

/** Monaco theme id for the app theme + editor preference. */
export function monacoTheme(pref: string, resolved: 'light' | 'dark'): string {
  switch (pref) {
    case 'light':
      return 'vs'
    case 'dark':
      return 'vs-dark'
    case 'high-contrast':
      return resolved === 'dark' ? 'hc-black' : 'hc-light'
    default:
      return resolved === 'dark' ? 'vs-dark' : 'vs'
  }
}
