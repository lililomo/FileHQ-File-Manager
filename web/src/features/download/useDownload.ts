import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Item } from '@/features/browser/api'
import { announce } from '@/lib/announce'
import { errorMessage } from '@/lib/errors'
import { signDownloadUrl, startDownload } from '@/lib/media-url'
import { createZip } from './api'

/** Download actions (PROMPT 12b task 6): one file through a fresh signed URL,
 *  folders or several items through a streamed zip. */
export function useDownload() {
  const { t } = useTranslation('files')

  const downloadFile = useCallback(
    async (item: Pick<Item, 'path' | 'name'>) => {
      try {
        const url = await signDownloadUrl(item.path)
        startDownload(url, item.name)
        announce(t('download.started', { name: item.name }))
      } catch (e) {
        toast.error(errorMessage(e))
      }
    },
    [t],
  )

  const downloadZip = useCallback(
    async (items: Pick<Item, 'path' | 'name' | 'isDir'>[], name?: string) => {
      const paths = items.map((i) => i.path)
      if (paths.length === 0) return
      const id = toast.loading(t('download.preparingZip', { count: paths.length }))
      try {
        const res = await createZip(paths, name ? { name } : {})
        toast.dismiss(id)
        startDownload(res.url, res.name)
        toast.success(t('download.zipStarted', { name: res.name }))
        announce(t('download.zipStarted', { name: res.name }))
      } catch (e) {
        toast.dismiss(id)
        toast.error(errorMessage(e))
      }
    },
    [t],
  )

  /** Download whatever is selected: a single file directly, anything else as zip. */
  const download = useCallback(
    (items: Pick<Item, 'path' | 'name' | 'isDir'>[]) => {
      if (items.length === 1 && !items[0]!.isDir) return downloadFile(items[0]!)
      return downloadZip(items)
    },
    [downloadFile, downloadZip],
  )

  return useMemo(
    () => ({ downloadFile, downloadZip, download }),
    [downloadFile, downloadZip, download],
  )
}
