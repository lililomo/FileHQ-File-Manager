import { Download, FileQuestion, Loader2 } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { Button } from '@/components/ui/button'
import { ApiError } from '@/lib/api'
import { formatBytes, formatDateTimeFull } from '@/lib/format'
import { startDownload } from '@/lib/media-url'
import { entryCategory, entryPreviewKind, signArchiveEntry, type ArchiveEntry } from './api'

export interface EntryPreviewProps {
  archive: string
  entry: ArchiveEntry
  session: string | undefined
  /** The entry needs a password the browser does not hold yet. */
  onPasswordRequired: () => void
}

type Loaded =
  | { kind: 'text'; text: string; truncated: boolean }
  | { kind: 'image'; url: string }
  | { kind: 'none' }
  | { kind: 'password' }
  | { kind: 'error'; message: string }

async function loadPreview(
  archive: string,
  entry: ArchiveEntry,
  session: string | undefined,
): Promise<Loaded> {
  const kind = entryPreviewKind(entry)
  if (kind === 'none') return { kind: 'none' }
  try {
    const signed = await signArchiveEntry(archive, entry.path, session)
    if (kind === 'image') return { kind: 'image', url: signed.url }
    const res = await fetch(signed.url, { credentials: 'omit' })
    if (!res.ok) {
      let code = ''
      let message = `${res.status}`
      try {
        const body = (await res.json()) as { error?: { code?: string; message?: string } }
        code = body.error?.code ?? ''
        message = body.error?.message ?? message
      } catch {
        /* not json */
      }
      if (code === 'ARCHIVE_PASSWORD_REQUIRED' || code === 'ARCHIVE_PASSWORD_WRONG')
        return { kind: 'password' }
      return { kind: 'error', message }
    }
    const text = await res.text()
    return { kind: 'text', text, truncated: text.length > 200_000 }
  } catch (e) {
    if (e instanceof ApiError && e.code === 'ARCHIVE_PASSWORD_REQUIRED') return { kind: 'password' }
    return { kind: 'error', message: e instanceof Error ? e.message : String(e) }
  }
}

/** Preview of one member (SPEC §8.11): text ≤ 1 MB and raster images ≤
 *  20 MB are streamed straight out of the archive through a signed
 *  entry-content URL; everything else offers a download. */
export function EntryPreview({ archive, entry, session, onPasswordRequired }: EntryPreviewProps) {
  const { t, i18n } = useTranslation(['archive', 'files', 'errors'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const query = useQuery({
    queryKey: ['archive-entry-preview', archive, entry.path, entry.size, session ?? ''],
    queryFn: () => loadPreview(archive, entry, session),
    staleTime: 60_000,
    retry: false,
  })
  const state: Loaded | { kind: 'loading' } = query.data ?? { kind: 'loading' }

  const download = async () => {
    try {
      const signed = await signArchiveEntry(archive, entry.path, session)
      startDownload(signed.url, entry.name)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'ARCHIVE_PASSWORD_REQUIRED') onPasswordRequired()
    }
  }

  return (
    <aside
      className="flex h-full min-h-0 flex-col border-l border-border bg-surface"
      aria-label={t('archive:browser.preview.label')}
      data-testid="archive-preview"
    >
      <div className="flex items-center gap-2 border-b border-border px-3 py-2 text-sm">
        <FileIcon category={toIconCategory(entryCategory(entry))} size={18} className="shrink-0" />
        <span className="min-w-0 flex-1 truncate font-medium" title={entry.path}>
          {entry.name}
        </span>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => void download()}
          aria-label={t('archive:browser.actions.downloadEntry')}
          data-testid="archive-preview-download"
        >
          <Download aria-hidden="true" />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-3 text-sm">
        {state.kind === 'loading' ? (
          <div className="flex items-center gap-2 text-text-muted" aria-busy="true">
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            {t('archive:browser.preview.loading')}
          </div>
        ) : state.kind === 'text' ? (
          <pre
            className="whitespace-pre-wrap break-words font-mono text-xs"
            data-testid="archive-preview-text"
          >
            {state.truncated ? state.text.slice(0, 200_000) : state.text}
            {state.truncated ? `\n…` : null}
          </pre>
        ) : state.kind === 'image' ? (
          <img
            src={state.url}
            alt={entry.name}
            className="max-h-full max-w-full object-contain"
            data-testid="archive-preview-image"
          />
        ) : state.kind === 'error' ? (
          <p className="text-danger" role="alert">
            {state.message}
          </p>
        ) : state.kind === 'password' ? (
          <div className="flex flex-col items-center gap-2 py-6 text-center text-text-muted">
            <p>{t('archive:browser.password.needed')}</p>
            <Button
              size="sm"
              variant="outline"
              onClick={onPasswordRequired}
              data-testid="archive-preview-password"
            >
              {t('archive:browser.password.enter')}
            </Button>
          </div>
        ) : (
          <div className="flex flex-col items-center gap-2 py-6 text-center text-text-muted">
            <FileQuestion className="size-8" aria-hidden="true" />
            <p>{t('archive:browser.preview.unavailable')}</p>
            <Button size="sm" variant="outline" onClick={() => void download()}>
              <Download aria-hidden="true" />
              {t('archive:browser.actions.downloadEntry')}
            </Button>
          </div>
        )}
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 border-t border-border px-3 py-2 text-xs text-text-muted">
        <dt>{t('archive:browser.columns.size')}</dt>
        <dd className="tabular">{formatBytes(entry.size, lang)}</dd>
        {entry.compressedSize >= 0 ? (
          <>
            <dt>{t('archive:browser.columns.compressed')}</dt>
            <dd className="tabular">{formatBytes(entry.compressedSize, lang)}</dd>
          </>
        ) : null}
        <dt>{t('archive:browser.columns.mtime')}</dt>
        <dd>{formatDateTimeFull(entry.mtime, lang)}</dd>
        {entry.method ? (
          <>
            <dt>{t('archive:browser.preview.method')}</dt>
            <dd>{entry.method}</dd>
          </>
        ) : null}
      </dl>
    </aside>
  )
}
