import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type { Item } from '@/features/browser/api'
import { Thumb } from '@/features/browser/views/Thumb'
import { cn } from '@/lib/utils'

/** Thumbnails rendered on each side of the active image; the strip scrolls
 *  horizontally and re-windows as the user moves (SPEC §8.14 "filmstrip
 *  thumbnail bawah (thumb 128, scroll ke aktif)"). */
export const FILMSTRIP_WINDOW = 60

export interface FilmstripProps {
  items: Item[]
  activeIndex: number
  onSelect: (item: Item) => void
}

export function Filmstrip({ items, activeIndex, onSelect }: FilmstripProps) {
  const { t } = useTranslation('viewer')
  const listRef = useRef<HTMLDivElement>(null)
  const start = Math.max(0, activeIndex - FILMSTRIP_WINDOW)
  const end = Math.min(items.length, activeIndex + FILMSTRIP_WINDOW + 1)
  const window = useMemo(() => items.slice(start, end), [items, start, end])
  const activePath = items[activeIndex]?.path

  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>('[data-active="true"]')
    el?.scrollIntoView({ inline: 'center', block: 'nearest', behavior: 'smooth' })
  }, [activePath])

  return (
    <div
      ref={listRef}
      className="flex h-[88px] shrink-0 items-center gap-1 overflow-x-auto border-t border-border bg-surface px-2"
      role="listbox"
      aria-label={t('image.filmstrip')}
      aria-orientation="horizontal"
      data-testid="viewer-filmstrip"
    >
      {window.map((it, i) => {
        const active = start + i === activeIndex
        return (
          <button
            key={it.path}
            type="button"
            role="option"
            aria-selected={active}
            aria-label={it.name}
            title={it.name}
            data-active={active ? 'true' : undefined}
            data-testid="filmstrip-item"
            onClick={() => onSelect(it)}
            className={cn(
              'flex size-[72px] shrink-0 items-center justify-center rounded-md border-2 border-transparent bg-surface-2 p-0.5 outline-none transition-colors',
              active ? 'border-accent' : 'hover:border-border focus-visible:border-focus',
            )}
          >
            <Thumb item={it} box={64} iconSize={28} />
          </button>
        )
      })}
    </div>
  )
}
