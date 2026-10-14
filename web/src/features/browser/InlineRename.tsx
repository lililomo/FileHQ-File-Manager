import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { splitStem, validateName } from '@/features/file-ops/nameValidation'
import { cn } from '@/lib/utils'
import type { Item } from './api'

export interface InlineRenameProps {
  item: Item
  /** Names of the other entries in the folder (duplicate check). */
  siblings: Iterable<string>
  onCommit: (newName: string) => void
  onCancel: () => void
  className?: string
}

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** In-place rename input (PROMPT 9b task 7): the stem is pre-selected, Enter
 *  commits, Esc cancels, blur commits; live validation mirrors the server. */
export function InlineRename({ item, siblings, onCommit, onCancel, className }: InlineRenameProps) {
  const { t } = useTranslation(['fileops', 'errors'])
  const lt = t as unknown as LooseT
  const [value, setValue] = useState(item.name)
  const ref = useRef<HTMLInputElement>(null)
  const finished = useRef(false)
  const siblingSet = new Set(siblings)
  siblingSet.delete(item.name)
  const check = validateName(value.trim(), siblingSet)
  const unchanged = value.trim() === item.name
  const errorText = check.error
    ? check.error === 'exists'
      ? t('fileops:name.exists')
      : lt(`errors:reason.${check.error}`, { defaultValue: '' }) || t('errors:NAME_INVALID')
    : null

  useEffect(() => {
    const el = ref.current
    if (!el) return
    el.focus()
    const { stem } = item.isDir ? { stem: item.name } : splitStem(item.name)
    el.setSelectionRange(0, stem.length)
  }, [item.isDir, item.name])

  const commit = () => {
    if (finished.current) return
    finished.current = true
    const v = value.trim()
    if (unchanged || check.error) onCancel()
    else onCommit(v)
  }
  const cancel = () => {
    if (finished.current) return
    finished.current = true
    onCancel()
  }

  return (
    <span
      className={cn('relative flex min-w-0 flex-1 items-center', className)}
      onClick={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
      onKeyUp={(e) => e.stopPropagation()}
      data-testid="inline-rename"
    >
      <input
        ref={ref}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key === 'Enter') {
            e.preventDefault()
            commit()
          } else if (e.key === 'Escape') {
            e.preventDefault()
            cancel()
          }
        }}
        onBlur={commit}
        aria-label={t('fileops:name.renameLabel', { name: item.name })}
        aria-invalid={errorText ? true : undefined}
        aria-describedby={errorText || check.windowsIncompatible ? 'inline-rename-hint' : undefined}
        autoComplete="off"
        spellCheck={false}
        className={cn(
          'h-6 w-full min-w-0 rounded border bg-bg px-1 text-sm text-text outline-none',
          errorText ? 'border-danger' : 'border-accent',
        )}
        data-testid="inline-rename-input"
      />
      {errorText || check.windowsIncompatible ? (
        <span
          id="inline-rename-hint"
          role={errorText ? 'alert' : 'note'}
          className={cn(
            'absolute top-full left-0 z-20 mt-0.5 max-w-64 rounded border border-border bg-bg px-1.5 py-0.5 text-xs shadow-md',
            errorText ? 'text-danger' : 'text-warning',
          )}
          data-testid="inline-rename-hint"
        >
          {errorText ?? t('fileops:name.windowsWarning')}
        </span>
      ) : null}
    </span>
  )
}
