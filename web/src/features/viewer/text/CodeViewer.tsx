import { useQuery } from '@tanstack/react-query'
import { Info, WrapText } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { IconButton } from '@/components/ui/icon-button'
import type { Item } from '@/features/browser/api'
import { cn } from '@/lib/utils'
import { highlightLines } from './highlight'
import { BinaryFileError, loadText, TEXT_HEAD_BYTES } from './source'
import { TextState } from './TextState'
import { VirtualRows } from './VirtualRows'

const LINE = 20

/** Read-only text/code viewer (SPEC §8.14 TextViewer): highlight.js per
 *  language (lazy), line numbers, wrap toggle; "Edit" lives in the overlay
 *  header. `note` shows a banner (HTML: live preview postponed to 23e). */
export function CodeViewer({ item, note }: { item: Item; note?: string }) {
  const { t } = useTranslation('viewer')
  const [wrap, setWrap] = useState(false)
  const src = useQuery({
    queryKey: ['viewer-text', item.path, item.etag],
    queryFn: ({ signal }) => loadText(item, signal),
    staleTime: 30_000,
    retry: false,
  })
  const language = src.data?.language ?? 'plaintext'
  const hl = useQuery({
    queryKey: ['viewer-hl', item.path, item.etag, language],
    queryFn: () => highlightLines(src.data!.text, language),
    enabled: !!src.data,
    staleTime: Infinity,
    retry: false,
  })
  const plain = useMemo(() => (src.data ? src.data.text.split(/\r\n|\r|\n/) : []), [src.data])

  if (src.isPending || (src.data && hl.isPending)) return <TextState kind="loading" />
  if (src.isError)
    return <TextState kind={src.error instanceof BinaryFileError ? 'binary' : 'error'} />
  const lines = hl.data ?? plain
  const html = !!hl.data
  const gutter = String(lines.length).length
  return (
    <div
      className="flex h-full min-h-0 flex-col"
      data-testid="code-viewer"
      data-language={language}
    >
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border bg-surface px-2 text-xs text-text-muted">
        <span className="tabular">{t('text.lines', { count: lines.length })}</span>
        <span>·</span>
        <span>{language}</span>
        {src.data?.encoding && src.data.encoding !== 'utf-8' ? (
          <>
            <span>·</span>
            <span>{src.data.encoding}</span>
          </>
        ) : null}
        <span className="flex-1" />
        <IconButton
          label={t('text.wrap')}
          onClick={() => setWrap((w) => !w)}
          aria-pressed={wrap}
          className={wrap ? 'bg-surface-2 text-text' : undefined}
          data-testid="text-wrap"
        >
          <WrapText aria-hidden="true" />
        </IconButton>
      </div>
      {note || src.data?.partial ? (
        <div
          className="flex shrink-0 items-start gap-2 border-b border-border bg-surface-2 px-3 py-2 text-xs text-text-muted"
          role="note"
        >
          <Info aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
          <span>
            {note}
            {note && src.data?.partial ? ' ' : ''}
            {src.data?.partial ? t('text.partial', { kb: TEXT_HEAD_BYTES / 1024 }) : ''}
          </span>
        </div>
      ) : null}
      <VirtualRows
        key={wrap ? 'wrap' : 'nowrap'}
        count={lines.length}
        rowHeight={LINE}
        measure={wrap}
        testId="code-lines"
        className="bg-bg font-mono text-[13px] leading-5"
        renderRow={(i) => (
          <div className="flex" data-line={i + 1}>
            <span
              className="sticky left-0 shrink-0 select-none border-r border-border bg-surface pr-2 pl-3 text-right text-text-muted tabular"
              style={{ width: `${gutter + 2}ch` }}
              aria-hidden="true"
            >
              {i + 1}
            </span>
            {html ? (
              <code
                className={cn(
                  'hljs block pr-4 pl-3',
                  wrap ? 'break-all whitespace-pre-wrap' : 'whitespace-pre',
                )}
                // highlight.js output sanitised by DOMPurify (span[class] only) in highlightLines
                dangerouslySetInnerHTML={{ __html: lines[i] || '\u200b' }}
              />
            ) : (
              <code
                className={cn(
                  'block pr-4 pl-3',
                  wrap ? 'break-all whitespace-pre-wrap' : 'whitespace-pre',
                )}
              >
                {lines[i] || '\u200b'}
              </code>
            )}
          </div>
        )}
      />
    </div>
  )
}
