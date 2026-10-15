import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { errorMessage } from '@/lib/errors'
import {
  deleteAllVersions,
  deleteVersion,
  restoreVersion,
  versionsQueryKey,
  type FileVersion,
} from './versionsApi'

/** Confirmations shared by the dialog and the Properties tab. */
export function useVersionActions(
  path: string,
  opts: { onRestore?: (v: FileVersion) => Promise<boolean> | boolean } = {},
) {
  const { t } = useTranslation('editor')
  const qc = useQueryClient()
  const [confirm, setConfirm] = useState<{
    kind: 'restore' | 'delete' | 'deleteAll'
    v?: FileVersion
  } | null>(null)
  const name = path.split('/').pop() ?? path
  const invalidate = () => void qc.invalidateQueries({ queryKey: versionsQueryKey(path) })
  const run = async () => {
    const c = confirm
    setConfirm(null)
    if (!c) return
    try {
      if (c.kind === 'restore' && c.v) {
        const ok = opts.onRestore ? await opts.onRestore(c.v) : false
        if (!opts.onRestore) {
          await restoreVersion(c.v.id)
          toast.success(t('versions.restored', { no: c.v.no, name }))
        } else if (!ok) return
      } else if (c.kind === 'delete' && c.v) {
        await deleteVersion(c.v.id)
        toast.success(t('versions.deleted'))
      } else if (c.kind === 'deleteAll') {
        await deleteAllVersions(path)
        toast.success(t('versions.deleted'))
      }
    } catch (e) {
      toast.error(errorMessage(e))
    }
    invalidate()
    void qc.invalidateQueries({ queryKey: ['list'] })
  }
  const dialog = (
    <AlertDialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)}>
      <AlertDialogContent data-testid="version-confirm">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {confirm?.kind === 'restore'
              ? t('versions.restoreTitle', { no: confirm.v?.no })
              : confirm?.kind === 'deleteAll'
                ? t('versions.deleteAllTitle', { name })
                : t('versions.delete')}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {confirm?.kind === 'restore'
              ? t('versions.restoreDesc', { no: confirm.v?.no, name })
              : confirm?.kind === 'deleteAll'
                ? t('versions.deleteAllDesc', { n: confirm.v?.no ?? '' })
                : t('versions.no', { no: confirm?.v?.no })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t('dialog.cancel')}</AlertDialogCancel>
          <AlertDialogAction onClick={() => void run()} data-testid="version-confirm-ok">
            {confirm?.kind === 'restore' ? t('versions.restore') : t('versions.delete')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
  return { confirm: setConfirm, dialog, invalidate }
}
