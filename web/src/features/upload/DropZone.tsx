import { useEffect, useState, type RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { hasOsFiles } from '@/features/file-ops/dnd'

/** Full-area overlay while OS files hover the content area (SPEC §8.8
 *  "Lepaskan untuk mengunggah ke <folder>"). It never takes the drop
 *  itself (pointer-events: none): the folder rows and the grid underneath
 *  decide where the files go, so dropping on a folder row still targets that
 *  folder. */
export function DropZone({
  container,
  path,
}: {
  container: RefObject<HTMLElement | null>
  path: string
}) {
  const { t } = useTranslation('upload')
  const [over, setOver] = useState(false)
  useEffect(() => {
    const el = container.current
    if (!el) return
    let depth = 0
    const enter = (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return
      depth++
      setOver(true)
    }
    const leave = (e: DragEvent) => {
      if (!hasOsFiles(e.dataTransfer)) return
      depth = Math.max(0, depth - 1)
      if (depth === 0) setOver(false)
    }
    const drop = () => {
      depth = 0
      setOver(false)
    }
    const end = () => {
      depth = 0
      setOver(false)
    }
    el.addEventListener('dragenter', enter)
    el.addEventListener('dragleave', leave)
    el.addEventListener('drop', drop)
    window.addEventListener('dragend', end)
    window.addEventListener('blur', end)
    return () => {
      el.removeEventListener('dragenter', enter)
      el.removeEventListener('dragleave', leave)
      el.removeEventListener('drop', drop)
      window.removeEventListener('dragend', end)
      window.removeEventListener('blur', end)
    }
  }, [container])
  if (!over) return null
  return (
    <div
      className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded-md border-2 border-dashed border-accent bg-accent/10"
      data-testid="upload-dropzone"
      aria-hidden="true"
    >
      <div className="rounded-md bg-surface px-4 py-2 text-sm font-medium text-text shadow">
        {t('dropzone', { path })}
      </div>
    </div>
  )
}
