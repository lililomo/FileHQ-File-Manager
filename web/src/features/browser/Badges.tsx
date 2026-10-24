import { Pin, Star, Users } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { Item } from './api'

/** Token classes per tag colour (`--tag-*` in tokens.css, light + dark). */
const TAG_DOT_CLASS: Record<string, string> = {
  red: 'bg-tag-red',
  orange: 'bg-tag-orange',
  yellow: 'bg-tag-yellow',
  green: 'bg-tag-green',
  blue: 'bg-tag-blue',
  purple: 'bg-tag-purple',
  gray: 'bg-tag-gray',
}

export function TagDot({ color, className }: { color: string; className?: string }) {
  const { t } = useTranslation('meta')
  return (
    <span
      className={cn('inline-block size-2.5 shrink-0 rounded-full', TAG_DOT_CLASS[color], className)}
      role="img"
      aria-label={t('badges.tag', { color: t(`tag.colors.${color}` as 'tag.colors.red') })}
      data-tag={color}
    />
  )
}

/** Name-column badges (SPEC §8.7): favourite star, pin, colour dot and the
 *  share badge placeholder (Step 18b fills `shared`). */
export function ItemBadges({ item, className }: { item: Item; className?: string }) {
  // most rows carry no badge: bail out before any hook (one subscription less
  // per visible Details row, Step 21)
  if (!item.favorite && !item.pinned && !item.tag && item.shared === 'none') return null
  return <Badges item={item} className={className} />
}

function Badges({ item, className }: { item: Item; className?: string | undefined }) {
  const { t } = useTranslation('meta')
  return (
    <span
      className={cn('inline-flex shrink-0 items-center gap-1', className)}
      data-testid="item-badges"
    >
      {item.tag ? <TagDot color={item.tag} /> : null}
      {item.pinned ? (
        <Pin
          className="size-3.5 text-text-muted"
          role="img"
          aria-label={t('badges.pinned')}
          data-badge="pinned"
        />
      ) : null}
      {item.favorite ? (
        <Star
          className="size-3.5 fill-star-fill text-star"
          role="img"
          aria-label={t('badges.favorite')}
          data-badge="favorite"
        />
      ) : null}
      {item.shared !== 'none' ? (
        <Users
          className="size-3.5 text-accent"
          role="img"
          aria-label={t('badges.shared')}
          data-badge="shared"
        />
      ) : null}
    </span>
  )
}
