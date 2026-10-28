import { useQuery } from '@tanstack/react-query'
import { ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Item } from '@/features/browser/api'
import { fetchSigned, getMediaUrl } from '@/lib/media-url'
import { publicContentOrigin } from '@/lib/public-scope'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/stores/session'
import { CodeViewer } from './CodeViewer'
import { htmlPreviewAvailable } from './htmlPreview'
import { TextState } from './TextState'

/** Signed inline URL of the document; a `403 SIGNATURE_EXPIRED` is renewed
 *  the way the media players do (one Range probe, then the cached fresh URL). */
async function previewUrl(path: string, signal?: AbortSignal): Promise<string> {
  const probe = await fetchSigned(path, 'inline', {
    headers: { Range: 'bytes=0-0' },
    ...(signal ? { signal } : {}),
  })
  if (!probe.ok) throw new Error(`HTTP ${probe.status}`)
  return getMediaUrl(path, 'inline')
}

/** HTML viewer (SPEC §8.14 "Preview HTML", Step 23e): with a content origin
 *  configured the document renders inside `<iframe sandbox="">` — no
 *  scripts, no same-origin, no forms, no network (the response also carries
 *  `CSP: sandbox; default-src 'none'`), served from the cookie-less content
 *  origin — with a "Tampilkan source" tab; without one only the source is
 *  shown (the v1 behaviour). */
export function HtmlPreview({ item }: { item: Item }) {
  const { t } = useTranslation('viewer')
  const config = useSessionStore((s) => s.config)
  const available = htmlPreviewAvailable(config?.contentBaseUrl || publicContentOrigin())
  const [tab, setTab] = useState<'preview' | 'source'>('preview')
  if (!available) return <CodeViewer item={item} note={t('text.htmlNote')} />
  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="html-viewer">
      <div
        className="flex h-9 shrink-0 items-center gap-1 border-b border-border bg-surface px-2"
        role="tablist"
        aria-label={t('text.htmlTabs')}
      >
        {(['preview', 'source'] as const).map((k) => (
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
            data-testid={`html-tab-${k}`}
          >
            {t(k === 'preview' ? 'text.htmlPreviewTab' : 'text.htmlSourceTab')}
          </button>
        ))}
      </div>
      {tab === 'preview' ? (
        <SandboxFrame item={item} />
      ) : (
        <div className="min-h-0 flex-1">
          <CodeViewer item={item} />
        </div>
      )}
    </div>
  )
}

function SandboxFrame({ item }: { item: Item }) {
  const { t } = useTranslation('viewer')
  const src = useQuery({
    queryKey: ['viewer-html-url', item.path, item.etag],
    queryFn: ({ signal }) => previewUrl(item.path, signal),
    staleTime: 60_000,
    retry: false,
  })
  if (src.isPending) return <TextState kind="loading" />
  if (src.isError) return <TextState kind="error" />
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <iframe
        // the sandbox is the whole point: no allow-scripts, no
        // allow-same-origin, no allow-forms, no allow-downloads
        sandbox=""
        referrerPolicy="no-referrer"
        src={src.data}
        title={t('text.htmlFrameTitle', { name: item.name })}
        className="min-h-0 flex-1 border-0 bg-white"
        data-testid="html-preview-frame"
      />
      <p
        className="flex shrink-0 items-center gap-2 border-t border-border bg-surface-2 px-3 py-1.5 text-xs text-text-muted"
        role="note"
        data-testid="html-sandbox-note"
      >
        <ShieldCheck aria-hidden="true" className="size-3.5 shrink-0" />
        {t('text.htmlSandboxNote')}
      </p>
    </div>
  )
}
