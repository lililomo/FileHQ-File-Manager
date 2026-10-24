import { Pin, Star } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TagDot } from '@/features/browser/Badges'
import type { Item } from '@/features/browser/api'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { cn } from '@/lib/utils'
import { TAG_COLORS, type TagColor } from './api'

/** Favourite / pin / tag controls shared by Properties and Quick Look (SPEC §8.7). */
export function MetaControls({
  item,
  onToggleFavorite,
  onTogglePin,
  onSetTag,
}: {
  item: Item
  onToggleFavorite?: ((item: Item) => void) | undefined
  onTogglePin?: ((item: Item) => void) | undefined
  onSetTag?: ((item: Item, color: TagColor | null) => void) | undefined
}) {
  const { t } = useTranslation('meta')
  if (item.broken) return null
  return (
    <div className="flex flex-wrap items-center gap-1" data-testid="meta-controls">
      {onToggleFavorite ? (
        <Button
          size="sm"
          variant={item.favorite ? 'secondary' : 'outline'}
          aria-pressed={item.favorite}
          onClick={() => onToggleFavorite(item)}
          data-testid="meta-favorite"
        >
          <Star className={cn(item.favorite && 'fill-star-fill text-star')} aria-hidden="true" />
          {t('badges.favorite')}
        </Button>
      ) : null}
      {onTogglePin ? (
        <Button
          size="sm"
          variant={item.pinned ? 'secondary' : 'outline'}
          aria-pressed={item.pinned}
          onClick={() => onTogglePin(item)}
          data-testid="meta-pin"
        >
          <Pin aria-hidden="true" />
          {t('badges.pinned')}
        </Button>
      ) : null}
      {onSetTag ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              size="sm"
              variant="outline"
              data-testid="meta-tag"
              data-tag={item.tag ?? undefined}
            >
              {item.tag ? <TagDot color={item.tag} /> : null}
              {item.tag ? t(`tag.colors.${item.tag}` as 'tag.colors.red') : t('tag.title')}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-40">
            <DropdownMenuItem onSelect={() => onSetTag(item, null)} data-testid="meta-tag-none">
              {t('tag.none')}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            {TAG_COLORS.map((c) => (
              <DropdownMenuItem
                key={c}
                onSelect={() => onSetTag(item, c)}
                data-testid={`meta-tag-${c}`}
              >
                <TagDot color={c} />
                {t(`tag.colors.${c}`)}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </div>
  )
}
