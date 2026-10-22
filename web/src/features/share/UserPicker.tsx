import { useQuery } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { SharePermission } from '@/lib/share-scope'
import { suggestUsers, type UserSuggestion } from './sharedApi'

export interface PickedUser {
  username: string
  displayName: string
  permission: SharePermission
}

/** Username autocomplete (≥ 2 characters, ≤ 8 hits of username + display name,
 *  SPEC §8.15 "autocomplete privasi") with one chip per recipient and the
 *  permission per chip. The owner's own name never appears (server-side). */
export function UserPicker({
  value,
  onChange,
  disabled,
}: {
  value: PickedUser[]
  onChange: (next: PickedUser[]) => void
  disabled?: boolean | undefined
}) {
  const { t } = useTranslation('share')
  const [q, setQ] = useState('')
  const [open, setOpen] = useState(false)
  const [activeState, setActiveState] = useState<{ q: string; i: number }>({ q: '', i: 0 })
  const listId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const debounced = useDebounced(q.trim(), 200)
  const query = useQuery({
    queryKey: ['user-suggest', debounced],
    queryFn: ({ signal }) => suggestUsers(debounced, signal),
    enabled: debounced.length >= 2,
    staleTime: 30_000,
    retry: false,
  })
  const taken = new Set(value.map((v) => v.username))
  const hits = (query.data?.items ?? []).filter((h) => !taken.has(h.username))
  // the highlighted row resets whenever the query changes (derived, no effect)
  const active = activeState.q === debounced ? activeState.i : 0
  const setActive = (next: number | ((prev: number) => number)) =>
    setActiveState({ q: debounced, i: typeof next === 'function' ? next(active) : next })

  const add = (h: UserSuggestion) => {
    onChange([...value, { username: h.username, displayName: h.displayName, permission: 'viewer' }])
    setQ('')
    setOpen(false)
    inputRef.current?.focus()
  }
  const remove = (username: string) => onChange(value.filter((v) => v.username !== username))
  const setPerm = (username: string, permission: SharePermission) =>
    onChange(value.map((v) => (v.username === username ? { ...v, permission } : v)))

  return (
    <div className="space-y-2" data-testid="user-picker">
      {value.length > 0 ? (
        <ul className="flex flex-wrap gap-2" aria-label={t('users.recipients')}>
          {value.map((v) => (
            <li
              key={v.username}
              className="flex items-center gap-1 rounded-full border border-border bg-surface-2 py-0.5 pr-1 pl-2 text-sm"
              data-testid="recipient-chip"
              data-username={v.username}
            >
              <span className="font-medium">{v.displayName || v.username}</span>
              <span className="text-xs text-text-muted">@{v.username}</span>
              <Select
                value={v.permission}
                onValueChange={(p) => setPerm(v.username, p as SharePermission)}
                disabled={disabled}
              >
                <SelectTrigger
                  className="h-6 w-24 border-0 bg-transparent px-1 text-xs"
                  aria-label={t('users.permissionOf', { name: v.username })}
                  data-testid="recipient-perm"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="viewer">{t('users.viewer')}</SelectItem>
                  <SelectItem value="editor">{t('users.editor')}</SelectItem>
                </SelectContent>
              </Select>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="size-6"
                aria-label={t('users.remove', { name: v.username })}
                onClick={() => remove(v.username)}
                disabled={disabled}
              >
                <X aria-hidden="true" />
              </Button>
            </li>
          ))}
        </ul>
      ) : null}
      <div className="relative">
        <Input
          ref={inputRef}
          value={q}
          onChange={(e) => {
            setQ(e.target.value)
            setOpen(true)
          }}
          onFocus={() => setOpen(true)}
          onBlur={() => window.setTimeout(() => setOpen(false), 150)}
          onKeyDown={(e) => {
            if (!open || hits.length === 0) return
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setActive((a) => Math.min(a + 1, hits.length - 1))
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setActive((a) => Math.max(a - 1, 0))
            } else if (e.key === 'Enter') {
              e.preventDefault()
              const h = hits[active]
              if (h) add(h)
            } else if (e.key === 'Escape') {
              setOpen(false)
            }
          }}
          placeholder={t('users.search')}
          aria-label={t('users.search')}
          aria-autocomplete="list"
          aria-controls={listId}
          aria-expanded={open && hits.length > 0}
          autoComplete="off"
          disabled={disabled}
          data-testid="user-picker-input"
        />
        <p className="mt-1 text-xs text-text-muted">{t('users.recipientsHint')}</p>
        {open && debounced.length >= 2 ? (
          <ul
            id={listId}
            role="listbox"
            className="absolute z-20 mt-1 max-h-48 w-full overflow-auto rounded-md border border-border bg-surface shadow-md"
            data-testid="user-picker-list"
          >
            {query.isPending ? (
              <li className="px-3 py-2 text-sm text-text-muted">…</li>
            ) : hits.length === 0 ? (
              <li className="px-3 py-2 text-sm text-text-muted">{t('users.noMatch')}</li>
            ) : (
              hits.map((h, i) => (
                <li
                  key={h.username}
                  role="option"
                  aria-selected={i === active}
                  className={`flex cursor-pointer items-center justify-between px-3 py-1.5 text-sm ${
                    i === active ? 'bg-surface-2' : ''
                  }`}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    add(h)
                  }}
                  onMouseEnter={() => setActive(i)}
                  data-testid="user-picker-option"
                >
                  <span>{h.displayName || h.username}</span>
                  <Badge variant="secondary">@{h.username}</Badge>
                </li>
              ))
            )}
          </ul>
        ) : null}
      </div>
    </div>
  )
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setV(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return v
}
