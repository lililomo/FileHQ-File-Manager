import { GripVertical, ListX, Music, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { cn } from '@/lib/utils'
import { usePlayerStore } from '@/stores/player'

/** Queue of the music player (SPEC §8.14 "panel playlist"): click plays,
 *  drag (or Alt+↑/↓) reorders, ✕ removes, "Kosongkan" clears. Shown in
 *  queue order; shuffle only changes what plays next. */
export function Playlist() {
  const { t } = useTranslation('viewer')
  const queue = usePlayerStore((s) => s.queue)
  const index = usePlayerStore((s) => s.index)
  const jump = usePlayerStore((s) => s.jump)
  const remove = usePlayerStore((s) => s.remove)
  const move = usePlayerStore((s) => s.move)
  const clear = usePlayerStore((s) => s.clear)
  const [drag, setDrag] = useState<number | null>(null)
  const [over, setOver] = useState<number | null>(null)

  return (
    <section className="flex min-h-0 flex-col" aria-label={t('audio.playlist')}>
      <div className="flex items-center justify-between gap-2 border-b border-border px-3 py-2">
        <h3 className="text-sm font-medium">
          {t('audio.playlist')} <span className="text-text-muted tabular">({queue.length})</span>
        </h3>
        <Button
          variant="ghost"
          size="sm"
          onClick={clear}
          disabled={queue.length === 0}
          data-testid="playlist-clear"
        >
          <ListX aria-hidden="true" />
          {t('audio.clear')}
        </Button>
      </div>
      {queue.length === 0 ? (
        <p className="p-4 text-sm text-text-muted">{t('audio.empty')}</p>
      ) : (
        <ol className="min-h-0 flex-1 overflow-auto py-1" data-testid="playlist">
          {queue.map((track, i) => (
            <li
              key={track.path}
              draggable
              onDragStart={(e) => {
                setDrag(i)
                e.dataTransfer.effectAllowed = 'move'
                e.dataTransfer.setData('text/plain', String(i))
              }}
              onDragOver={(e) => {
                if (drag === null) return
                e.preventDefault()
                setOver(i)
              }}
              onDrop={(e) => {
                e.preventDefault()
                if (drag !== null) move(drag, i)
                setDrag(null)
                setOver(null)
              }}
              onDragEnd={() => {
                setDrag(null)
                setOver(null)
              }}
              className={cn(
                'group flex items-center gap-1 px-1 text-sm',
                i === index && 'bg-accent-subtle text-text',
                over === i &&
                  drag !== null &&
                  drag !== i &&
                  'outline-2 -outline-offset-2 outline-accent',
              )}
              data-testid="playlist-item"
              data-current={i === index ? 'true' : undefined}
            >
              <GripVertical
                aria-hidden="true"
                className="size-4 shrink-0 cursor-grab text-text-muted"
              />
              <button
                type="button"
                className="flex min-w-0 flex-1 items-center gap-2 rounded px-1 py-1.5 text-left hover:bg-surface-2 focus-visible:outline-2 focus-visible:outline-accent"
                onClick={() => jump(i)}
                onKeyDown={(e) => {
                  if (e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
                    e.preventDefault()
                    e.stopPropagation()
                    const to = e.key === 'ArrowUp' ? i - 1 : i + 1
                    if (to >= 0 && to < queue.length) move(i, to)
                  }
                }}
                aria-current={i === index ? 'true' : undefined}
                aria-keyshortcuts="Alt+ArrowUp Alt+ArrowDown"
              >
                <Music
                  aria-hidden="true"
                  className={cn('size-4 shrink-0', i === index ? 'text-accent' : 'text-text-muted')}
                />
                <span className="truncate" title={track.path}>
                  {track.name}
                </span>
              </button>
              <IconButton
                label={t('audio.remove', { name: track.name })}
                tooltipSide="left"
                className="size-7 opacity-60 group-hover:opacity-100 focus-visible:opacity-100"
                onClick={() => remove(i)}
                data-testid="playlist-remove"
              >
                <X aria-hidden="true" />
              </IconButton>
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}
