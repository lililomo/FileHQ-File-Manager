import { ChevronDown, ChevronUp, Loader2, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'

export type FindUiState = 'idle' | 'pending' | 'found' | 'notfound' | 'wrapped'

export interface PdfSearchProps {
  /** `again` = next/previous of the same query, '' = new query */
  onFind: (query: string, type: '' | 'again', previous: boolean) => void
  matches: { current: number; total: number } | null
  state: FindUiState
  onClose: () => void
}

/** Find bar of the PDF viewer (SPEC §8.14: PDFFindController, highlight all,
 *  next/prev). Typing searches after a short pause; Enter / Shift+Enter step. */
export function PdfSearch({ onFind, matches, state, onClose }: PdfSearchProps) {
  const { t } = useTranslation('viewer')
  const [query, setQuery] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    inputRef.current?.focus()
  }, [])
  useEffect(() => {
    const id = setTimeout(() => onFind(query, '', false), 250)
    return () => clearTimeout(id)
  }, [query, onFind])

  const has = query.trim().length > 0
  return (
    <div
      className="flex h-10 shrink-0 items-center gap-1 border-b border-border bg-surface px-2"
      role="search"
      aria-label={t('pdf.search')}
      data-testid="pdf-search"
      data-escape-local=""
    >
      <Input
        ref={inputRef}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            onFind(query, 'again', e.shiftKey)
          } else if (e.key === 'Escape') {
            e.preventDefault()
            onClose()
          }
        }}
        placeholder={t('pdf.searchPlaceholder')}
        aria-label={t('pdf.search')}
        className="h-7 w-64 text-sm"
        data-testid="pdf-search-input"
      />
      <IconButton
        label={t('pdf.searchPrev')}
        size="icon-sm"
        disabled={!has}
        onClick={() => onFind(query, 'again', true)}
        data-testid="pdf-search-prev"
      >
        <ChevronUp aria-hidden="true" />
      </IconButton>
      <IconButton
        label={t('pdf.searchNext')}
        size="icon-sm"
        disabled={!has}
        onClick={() => onFind(query, 'again', false)}
        data-testid="pdf-search-next"
      >
        <ChevronDown aria-hidden="true" />
      </IconButton>
      <span
        className="min-w-24 text-xs text-text-muted tabular"
        aria-live="polite"
        data-testid="pdf-search-count"
      >
        {!has ? null : state === 'pending' ? (
          <span className="inline-flex items-center gap-1">
            <Loader2 className="size-3 animate-spin" aria-hidden="true" />
            {t('pdf.searching')}
          </span>
        ) : state === 'notfound' || (matches && matches.total === 0) ? (
          t('pdf.noMatches')
        ) : matches ? (
          t('pdf.matches', { current: matches.current, total: matches.total })
        ) : null}
      </span>
      <IconButton label={t('pdf.searchClose')} size="icon-sm" onClick={onClose}>
        <X aria-hidden="true" />
      </IconButton>
    </div>
  )
}
