import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { BinaryFileError, fetchHead } from './source'
import { TextState } from './TextState'

/** Bytes fetched for a compact preview (enough for 200 ordinary lines). */
const HEAD_BYTES = 64 * 1024

/** First `lines` lines of a text file (Quick Look: 200; preview panel: 40),
 *  plain and unhighlighted so it opens fast. */
export function TextHead({
  path,
  etag,
  lines = 200,
  className,
}: {
  path: string
  etag?: string
  lines?: number
  className?: string
}) {
  const { t } = useTranslation('viewer')
  const head = useQuery({
    queryKey: ['viewer-head', path, etag],
    queryFn: ({ signal }) => fetchHead(path, HEAD_BYTES, signal),
    staleTime: 30_000,
    retry: false,
  })
  if (head.isPending) return <TextState kind="loading" />
  if (head.isError)
    return <TextState kind={head.error instanceof BinaryFileError ? 'binary' : 'error'} />
  const all = head.data.split(/\r\n|\r|\n/)
  const shown = all.slice(0, lines)
  const width = String(shown.length).length + 1
  return (
    <div className={cn('h-full overflow-auto bg-bg', className)} data-testid="text-head">
      <pre className="py-2 font-mono text-xs leading-5">
        {shown.map((l, i) => (
          <div key={i} className="flex">
            <span
              className="shrink-0 select-none pr-3 pl-2 text-right text-text-muted tabular"
              style={{ width: `${width + 2}ch` }}
              aria-hidden="true"
            >
              {i + 1}
            </span>
            <code className="whitespace-pre">{l || '\u200b'}</code>
          </div>
        ))}
      </pre>
      {all.length > lines ? (
        <p className="px-3 pb-3 text-xs text-text-muted">{t('quick.moreLines', { n: lines })}</p>
      ) : null}
    </div>
  )
}
