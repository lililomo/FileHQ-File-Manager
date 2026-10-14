import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Item } from '@/features/browser/api'
import { parentOf } from '@/features/file-ops/dnd'
import { errorMessage, patchLists } from '@/features/file-ops/useFileOps'
import { announce } from '@/lib/announce'
import { ApiError } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import {
  addFavorites,
  addPins,
  FAVORITES_QUERY_KEY,
  removeFavorites,
  removeNote,
  removePins,
  removeTags,
  setNote as setNoteApi,
  setTags,
  type TagColor,
} from './api'

type LooseT = (key: string, options?: Record<string, unknown>) => string

export interface MetaOps {
  /** Adds every path that is not a favourite yet, or removes all when all are. */
  toggleFavorite: (items: Item[]) => Promise<void>
  addFavorites: (paths: string[]) => Promise<void>
  removeFavorites: (paths: string[]) => Promise<void>
  togglePin: (items: Item[]) => Promise<void>
  setNote: (path: string, note: string) => Promise<boolean>
  /** `null` clears the tag. */
  setTag: (paths: string[], color: TagColor | null) => Promise<void>
}

/** Favourite / pin / note / tag mutations with optimistic listing patches
 *  (SPEC §8.7). The server invalidates its own listing cache; here every
 *  cached page of the parent folders is patched then refetched. */
export function useMeta(): MetaOps {
  const qc = useQueryClient()
  const { t, i18n } = useTranslation(['meta', 'errors', 'fileops'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const lt = t as unknown as LooseT
  const n = useCallback((count: number) => formatNumber(count, lang), [lang])

  const patch = useCallback(
    (paths: string[], fn: (it: Item) => Item) => {
      const set = new Set(paths)
      for (const d of new Set(paths.map(parentOf)))
        patchLists(qc, d, (items) => items.map((it) => (set.has(it.path) ? fn(it) : it)))
    },
    [qc],
  )
  const settle = useCallback(
    (paths: string[]) => {
      for (const d of new Set(paths.map(parentOf)))
        void qc.invalidateQueries({ queryKey: ['list', d] })
      for (const p of paths) void qc.invalidateQueries({ queryKey: ['stat', p] })
      void qc.invalidateQueries({ queryKey: FAVORITES_QUERY_KEY })
    },
    [qc],
  )
  const fail = useCallback(
    (e: unknown) => {
      let msg = errorMessage(lt, e)
      if (e instanceof ApiError && e.details.reason === 'pin_limit')
        msg = t('meta:pin.limit', { max: 50 })
      toast.error(msg)
      announce(msg, true)
    },
    [lt, t],
  )
  const ok = useCallback((msg: string) => {
    toast.success(msg)
    announce(msg)
  }, [])

  const add = useCallback<MetaOps['addFavorites']>(
    async (paths) => {
      if (paths.length === 0) return
      patch(paths, (it) => ({ ...it, favorite: true }))
      try {
        await addFavorites(paths)
        ok(t('meta:favorite.added', { count: paths.length, n: n(paths.length) }))
      } catch (e) {
        patch(paths, (it) => ({ ...it, favorite: false }))
        fail(e)
      } finally {
        settle(paths)
      }
    },
    [patch, settle, ok, fail, t, n],
  )
  const remove = useCallback<MetaOps['removeFavorites']>(
    async (paths) => {
      if (paths.length === 0) return
      patch(paths, (it) => ({ ...it, favorite: false }))
      try {
        await removeFavorites(paths)
        ok(t('meta:favorite.removed', { count: paths.length, n: n(paths.length) }))
      } catch (e) {
        patch(paths, (it) => ({ ...it, favorite: true }))
        fail(e)
      } finally {
        settle(paths)
      }
    },
    [patch, settle, ok, fail, t, n],
  )
  const toggleFavorite = useCallback<MetaOps['toggleFavorite']>(
    (items) => {
      if (items.length === 0) return Promise.resolve()
      const all = items.every((it) => it.favorite)
      const paths = items.map((it) => it.path)
      return all ? remove(paths) : add(paths.filter((_, i) => !items[i]!.favorite))
    },
    [add, remove],
  )

  const togglePin = useCallback<MetaOps['togglePin']>(
    async (items) => {
      if (items.length === 0) return
      const all = items.every((it) => it.pinned)
      const targets = all ? items : items.filter((it) => !it.pinned)
      const paths = targets.map((it) => it.path)
      patch(paths, (it) => ({ ...it, pinned: !all }))
      try {
        if (all) await removePins(paths)
        else await addPins(paths)
        ok(
          t(all ? 'meta:pin.removed' : 'meta:pin.added', {
            count: paths.length,
            n: n(paths.length),
          }),
        )
      } catch (e) {
        patch(paths, (it) => ({ ...it, pinned: all }))
        fail(e)
      } finally {
        settle(paths)
      }
    },
    [patch, settle, ok, fail, t, n],
  )

  const setNote = useCallback<MetaOps['setNote']>(
    async (path, note) => {
      const trimmed = note.trim()
      patch([path], (it) => ({ ...it, note: trimmed || null }))
      try {
        if (trimmed) await setNoteApi(path, trimmed)
        else await removeNote(path)
        ok(t(trimmed ? 'meta:note.saved' : 'meta:note.removed'))
        return true
      } catch (e) {
        fail(e)
        return false
      } finally {
        settle([path])
      }
    },
    [patch, settle, ok, fail, t],
  )

  const setTag = useCallback<MetaOps['setTag']>(
    async (paths, color) => {
      if (paths.length === 0) return
      patch(paths, (it) => ({ ...it, tag: color }))
      try {
        if (color) await setTags(paths, color)
        else await removeTags(paths)
        ok(
          color
            ? t('meta:tag.set', {
                count: paths.length,
                n: n(paths.length),
                color: t(`meta:tag.colors.${color}`),
              })
            : t('meta:tag.removed', { count: paths.length, n: n(paths.length) }),
        )
      } catch (e) {
        fail(e)
      } finally {
        settle(paths)
      }
    },
    [patch, settle, ok, fail, t, n],
  )

  return useMemo(
    () => ({
      toggleFavorite,
      addFavorites: add,
      removeFavorites: remove,
      togglePin,
      setNote,
      setTag,
    }),
    [toggleFavorite, add, remove, togglePin, setNote, setTag],
  )
}
