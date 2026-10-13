import { Search, X } from 'lucide-react'
import { forwardRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Kbd } from '@/components/ui/kbd'
import { cn } from '@/lib/utils'

export interface QuickFilterProps {
  value: string
  onChange: (value: string) => void
  /** Listing is paged and not fully loaded: offer the server search (Step 15). */
  incomplete?: boolean
  onServerSearch?: () => void
  className?: string
}

/** Client-side filter of the loaded items (PROMPT 7b task 6). Ctrl+F focuses it. */
export const QuickFilter = forwardRef<HTMLInputElement, QuickFilterProps>(function QuickFilter(
  { value, onChange, incomplete = false, onServerSearch, className },
  ref,
) {
  const { t } = useTranslation('files')
  return (
    <div className={cn('relative flex shrink-0 items-center', className)}>
      <Search
        className="pointer-events-none absolute left-2 size-3.5 text-text-muted"
        aria-hidden="true"
      />
      <Input
        ref={ref}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            if (value) onChange('')
            else (e.currentTarget as HTMLInputElement).blur()
          }
        }}
        placeholder={t('filter.placeholder')}
        aria-label={t('filter.label')}
        className="h-7 w-44 pr-7 pl-7 text-sm"
        spellCheck={false}
        autoComplete="off"
        data-testid="quick-filter"
      />
      {value ? (
        <Button
          variant="ghost"
          size="icon-sm"
          className="absolute right-0.5 size-6 text-text-muted"
          aria-label={t('filter.clear')}
          onClick={() => onChange('')}
        >
          <X aria-hidden="true" />
        </Button>
      ) : (
        <Kbd className="pointer-events-none absolute right-1.5 hidden md:inline-flex">
          {t('filter.kbd')}
        </Kbd>
      )}
      {value && incomplete && onServerSearch ? (
        <Button
          variant="link"
          size="sm"
          className="ml-1 h-7 px-1 text-xs"
          onClick={onServerSearch}
          data-testid="filter-server-search"
        >
          {t('filter.serverSearch')}
        </Button>
      ) : null}
    </div>
  )
})
