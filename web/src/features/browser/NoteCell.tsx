import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import i18n from '@/i18n'
import { MAX_NOTE_CHARS } from '@/features/meta/api'
import { cn } from '@/lib/utils'
import type { Item } from './api'

export interface NoteCellProps {
  item: Item
  /** Save handler; resolves false when the server rejected the note. */
  onSave?: ((path: string, note: string) => Promise<boolean>) | undefined
}

/** "Catatan" column cell (SPEC §8.7): plain text, click/Enter opens an inline
 *  input, Enter saves, Esc cancels; the limit is enforced live. The read-only
 *  state is hook-light (one useState, no i18n subscription): it renders in
 *  every visible Details row (Step 21 scroll budget). */
export function NoteCell({ item, onSave }: NoteCellProps) {
  const [editing, setEditing] = useState(false)
  const editable = !!onSave && !item.broken
  const start = () => {
    if (editable) setEditing(true)
  }
  if (editing) return <NoteEditor item={item} onSave={onSave} onDone={() => setEditing(false)} />
  return (
    <span
      className={cn(
        'block min-h-5 min-w-0 flex-1 truncate text-text-muted',
        editable && 'cursor-text rounded px-0.5 hover:bg-surface-2 hover:text-text',
      )}
      title={item.note ?? (editable ? i18n.t('meta:note.edit') : undefined)}
      tabIndex={-1}
      onClick={(e) => {
        if (!editable) return
        // a second click on the note cell of a selected row starts editing
        if (e.detail >= 2 || e.currentTarget.closest('[aria-selected="true"]')) {
          e.stopPropagation()
          start()
        }
      }}
      onDoubleClick={(e) => {
        if (!editable) return
        e.stopPropagation()
        start()
      }}
      onKeyDown={(e) => {
        if (editable && e.key === 'Enter') {
          e.stopPropagation()
          start()
        }
      }}
      data-testid="note-cell"
      data-editable={editable || undefined}
    >
      {item.note ?? ''}
    </span>
  )
}

function NoteEditor({
  item,
  onSave,
  onDone,
}: NoteCellProps & {
  onDone: () => void
}) {
  const { t } = useTranslation('meta')
  const [value, setValue] = useState(item.note ?? '')
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    inputRef.current?.select()
  }, [])
  const setEditing = (on: boolean) => {
    if (!on) onDone()
  }
  const commit = async () => {
    setEditing(false)
    if (!onSave) return
    const next = value.trim()
    if (next === (item.note ?? '')) return
    await onSave(item.path, next)
  }
  const tooLong = value.length > MAX_NOTE_CHARS

  return (
    <span className="flex min-w-0 flex-1 items-center gap-1" data-testid="note-editor">
      <input
        ref={inputRef}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        maxLength={MAX_NOTE_CHARS + 1}
        aria-label={t('note.edit')}
        aria-invalid={tooLong || undefined}
        placeholder={t('note.placeholder')}
        className={cn(
          'h-6 min-w-0 flex-1 rounded border border-accent bg-surface px-1 text-sm text-text outline-none',
          tooLong && 'border-danger',
        )}
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key === 'Enter' && !tooLong) {
            e.preventDefault()
            void commit()
          } else if (e.key === 'Escape') {
            e.preventDefault()
            setEditing(false)
          }
        }}
        onBlur={() => {
          if (!tooLong) void commit()
          else setEditing(false)
        }}
        data-testid="note-input"
      />
      <span
        className={cn('text-[10px] tabular', tooLong ? 'text-danger' : 'text-text-muted')}
        aria-live="polite"
      >
        {t('note.counter', { n: value.length, max: MAX_NOTE_CHARS })}
      </span>
    </span>
  )
}
