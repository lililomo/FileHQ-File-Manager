import { useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { QUOTA_QUERY_KEY } from '@/features/quota/api'
import { announce } from '@/lib/announce'
import { formatNumber } from '@/lib/format'
import { useTransfersStore } from '@/stores/transfers'
import { transferErrorMessage, uploader } from './useUploader'

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** Mount once in the shell: refreshes listings/quota when a batch finishes,
 *  shows the summary toast and offers to resume transfers left over from a
 *  previous page load. */
export function useUploaderEffects(onPickAgain: () => void): void {
  const qc = useQueryClient()
  const { i18n } = useTranslation(['upload'])
  useEffect(() => {
    const fixed = i18n.getFixedT(null, null) as unknown as LooseT
    const lt: LooseT = (key, options) => (options ? fixed(key, options) : fixed(key))
    const langOf = () => i18n.resolvedLanguage ?? 'id'
    return uploader.subscribe((ev) => {
      if (ev.type === 'done') {
        const dir = ev.transfer.finalPath
          ? ev.transfer.finalPath.slice(0, ev.transfer.finalPath.lastIndexOf('/')) || '/'
          : ev.transfer.dest
        void qc.invalidateQueries({ queryKey: ['list', dir] })
        if (dir !== ev.transfer.dest)
          void qc.invalidateQueries({ queryKey: ['list', ev.transfer.dest] })
        return
      }
      if (ev.type === 'error') {
        const msg = transferErrorMessage(ev.transfer)
        toast.error(ev.transfer.name ? `${ev.transfer.name}: ${msg}` : msg)
        announce(msg, true)
        return
      }
      for (const d of ev.dests) void qc.invalidateQueries({ queryKey: ['list', d] })
      void qc.invalidateQueries({ queryKey: QUOTA_QUERY_KEY })
      const lang = langOf()
      if (ev.done > 0) {
        const msg = lt('upload:toast.done', { count: ev.done, n: formatNumber(ev.done, lang) })
        const extra = [
          ev.skipped > 0 ? lt('upload:toast.skipped', { n: formatNumber(ev.skipped, lang) }) : '',
          ev.errors > 0 ? lt('upload:toast.errors', { n: formatNumber(ev.errors, lang) }) : '',
        ]
          .filter(Boolean)
          .join(' ')
        toast.success(extra ? `${msg} ${extra}` : msg)
        announce(msg)
      } else if (ev.skipped > 0 && ev.errors === 0) {
        toast.info(lt('upload:toast.allSkipped', { n: formatNumber(ev.skipped, lang) }))
      }
    })
  }, [qc, i18n])
  // leftovers from a previous page load (SPEC §8.8 step 7)
  useEffect(() => {
    const s = useTransfersStore.getState()
    const detached = s.order.filter((id) => s.items[id]?.detached).length
    if (detached === 0) return
    const fixed = i18n.getFixedT(null, null) as unknown as LooseT
    const lang = i18n.resolvedLanguage ?? 'id'
    toast(
      fixed('upload:toast.resumePrompt', { count: detached, n: formatNumber(detached, lang) }),
      {
        id: 'upload-resume',
        duration: 15_000,
        action: { label: fixed('upload:actions.pickAgain'), onClick: onPickAgain },
      },
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per page load
  }, [])
}
