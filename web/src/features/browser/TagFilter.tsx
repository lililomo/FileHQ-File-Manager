import { Check, Tag } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { TAG_COLORS, type TagColor } from '@/features/meta/api'
import { cn } from '@/lib/utils'
import { TagDot } from './Badges'

/** Toolbar chip "Tag: merah" (SPEC §8.7): server-side `?tag=` filter,
 *  combinable with the quick filter. */
export function TagFilter({
  value,
  onChange,
  compact = false,
}: {
  value: TagColor | null
  onChange: (next: TagColor | null) => void
  compact?: boolean
}) {
  const { t } = useTranslation('meta')
  const label = value ? t('tag.filterActive', { color: t(`tag.colors.${value}`) }) : t('tag.filter')
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant={value ? 'secondary' : 'ghost'}
          size="sm"
          className={cn('h-8 gap-1.5', value && 'rounded-full')}
          aria-label={label}
          aria-pressed={!!value}
          data-testid="tag-filter"
          data-tag={value ?? undefined}
        >
          {value ? <TagDot color={value} /> : <Tag aria-hidden="true" />}
          {!compact ? (
            <span className="text-xs">{value ? t(`tag.colors.${value}`) : t('tag.filter')}</span>
          ) : null}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem onSelect={() => onChange(null)} data-testid="tag-filter-all">
          {value === null ? <Check aria-hidden="true" /> : <span className="size-4" />}
          {t('tag.filterAll')}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        {TAG_COLORS.map((c) => (
          <DropdownMenuItem key={c} onSelect={() => onChange(c)} data-testid={`tag-filter-${c}`}>
            {value === c ? <Check aria-hidden="true" /> : <span className="size-4" />}
            <TagDot color={c} />
            {t(`tag.colors.${c}`)}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
