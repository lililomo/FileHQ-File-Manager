import { FileIcon } from '@/components/file-icon/FileIcon'
import { toIconCategory } from '@/components/file-icon/categories'
import { cn } from '@/lib/utils'
import type { Item } from '../api'
import { thumbSizeFor } from './gridMath'
import { useThumb } from './useThumb'

export interface ThumbProps {
  item: Item
  /** Rendered box (px). The server size is chosen ≥ box × devicePixelRatio. */
  box: number
  iconSize?: number
  className?: string
}

/** Thumbnail with the type icon as placeholder/fallback (SPEC §8.4): the
 *  <img> only gets its src after the signed URL loaded through the slot
 *  queue, so the browser serves it from cache instantly. */
export function Thumb({ item, box, iconSize, className }: ThumbProps) {
  const size = thumbSizeFor(box)
  const [ref, state] = useThumb(item, size)
  const icon = (
    <FileIcon
      category={item.isDir ? 'folder' : toIconCategory(item.category)}
      size={iconSize ?? Math.max(16, Math.round(box * 0.5))}
    />
  )
  return (
    <div
      ref={ref}
      className={cn('relative flex items-center justify-center overflow-hidden', className)}
      style={{ width: box, height: box }}
      data-thumb={state.status}
    >
      {state.status === 'ready' && state.src ? (
        <img
          src={state.src}
          alt=""
          loading="lazy"
          decoding="async"
          draggable={false}
          className="max-h-full max-w-full rounded-[3px] object-contain shadow-sm"
          data-testid="thumb-img"
        />
      ) : (
        icon
      )}
    </div>
  )
}
