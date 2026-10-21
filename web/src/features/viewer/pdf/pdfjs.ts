/** Lazy pdf.js loader (SPEC §8.14 PdfViewer, §10.3): the library, the viewer
 *  components and their stylesheet form one lazy chunk (`pdfjs`), the worker
 *  is a self-hosted module file (`?url`), and the run-time assets (CMaps,
 *  standard fonts, WASM decoders, annotation icons) are served from
 *  `/pdfjs/` (copied by scripts/pdfjs-assets.mjs). */
import type { DocumentInitParameters } from 'pdfjs-dist/types/src/display/api'

export type PdfjsLib = typeof import('pdfjs-dist')
export type PdfjsViewer = typeof import('pdfjs-dist/web/pdf_viewer.mjs')

export interface Pdfjs {
  lib: PdfjsLib
  viewer: PdfjsViewer
}

let loading: Promise<Pdfjs> | null = null

export function loadPdfjs(): Promise<Pdfjs> {
  loading ??= (async () => {
    // pdf_viewer.mjs reads `globalThis.pdfjsLib` while it evaluates, so the
    // library must be loaded (and exposed) before the viewer chunk starts
    const lib = await import('pdfjs-dist')
    const g = globalThis as { pdfjsLib?: PdfjsLib }
    g.pdfjsLib ??= lib
    const [viewer, worker] = await Promise.all([
      import('pdfjs-dist/web/pdf_viewer.mjs'),
      import('pdfjs-dist/build/pdf.worker.min.mjs?url'),
      import('pdfjs-dist/web/pdf_viewer.css'),
      import('./pdf.css'),
    ])
    lib.GlobalWorkerOptions.workerSrc = worker.default
    return { lib, viewer }
  })()
  return loading
}

/** Same-origin run-time asset root (see scripts/pdfjs-assets.mjs). */
export const PDF_ASSETS = '/pdfjs/'

/** Document options shared by every load (SPEC §8.14: Range streaming,
 *  64 KB chunks, no auto-fetch of the whole file, no XFA; pdf.js 6 no longer
 *  has an eval path, so CSP script-src stays without unsafe-eval). */
export const PDF_DOC_OPTIONS: Partial<DocumentInitParameters> = {
  cMapUrl: `${PDF_ASSETS}cmaps/`,
  cMapPacked: true,
  standardFontDataUrl: `${PDF_ASSETS}standard_fonts/`,
  wasmUrl: `${PDF_ASSETS}wasm/`,
  iccUrl: `${PDF_ASSETS}iccs/`,
  rangeChunkSize: 65536,
  disableAutoFetch: true,
  withCredentials: false,
  enableXfa: false,
}

/** Minimal L10n for the viewer components: pdf.js only needs the language,
 *  direction and a passthrough `get`; nothing is fetched. */
export function nullL10n(lang: string) {
  const dir = 'ltr'
  return {
    getLanguage: () => lang,
    getDirection: () => dir,
    async get(ids: string | string[], _args?: unknown, fallback?: string): Promise<string> {
      if (Array.isArray(ids)) return ids.join(',')
      return fallback ?? ids
    },
    async translate(): Promise<void> {},
    async translateOnce(): Promise<void> {},
    async destroy(): Promise<void> {},
    pause(): void {},
    resume(): void {},
  }
}

/** Pages printed at most (SPEC §8.14 "batasi 200 halaman"). */
export const PRINT_MAX_PAGES = 200
