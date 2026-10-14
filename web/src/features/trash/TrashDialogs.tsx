import { useState } from 'react'
import { useTranslation } from 'react-i18next'
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
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { normalizeTypedPath, ROOT } from '@/features/browser/path'
import { PERMANENT_CONFIRM_WORD } from '@/features/file-ops/api'
import { FolderPicker } from '@/features/file-ops/FolderPicker'
import { formatBytes, formatNumber } from '@/lib/format'

/** "Pulihkan ke…": pick a destination folder for the selected trash items. */
export function RestoreToDialog({
  count,
  onClose,
  onSubmit,
}: {
  count: number
  onClose: () => void
  onSubmit: (to: string) => void
}) {
  const { t, i18n } = useTranslation(['trash', 'fileops'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const [dest, setDest] = useState(ROOT)
  const [typed, setTyped] = useState(ROOT)
  const [error, setError] = useState<string | null>(null)
  const choose = (p: string) => {
    setDest(p)
    setTyped(p)
    setError(null)
  }
  const submit = () => {
    const r = normalizeTypedPath(typed, ROOT)
    if (!r.ok) {
      setError(t('fileops:moveCopy.invalidPath'))
      return
    }
    onSubmit(r.path)
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg" data-testid="restore-to">
        <form
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
          className="space-y-3"
        >
          <DialogHeader>
            <DialogTitle>{t('trash:restoreTo.title')}</DialogTitle>
            <DialogDescription>
              {t('trash:restoreTo.description', { count, n: formatNumber(count, lang) })}
            </DialogDescription>
          </DialogHeader>
          <FolderPicker
            value={dest}
            onChange={choose}
            onActivate={(p) => {
              choose(p)
              onSubmit(p)
            }}
            className="h-64"
            autoFocus
          />
          <div className="space-y-1">
            <Label htmlFor="restore-to-path">{t('trash:restoreTo.destination')}</Label>
            <Input
              id="restore-to-path"
              value={typed}
              onChange={(e) => {
                setTyped(e.target.value)
                setError(null)
              }}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={!!error}
              data-testid="restore-to-path"
            />
            {error ? (
              <p className="text-xs text-danger" role="alert">
                {error}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('fileops:cancel')}
            </Button>
            <Button type="submit" data-testid="restore-to-submit">
              {t('trash:restoreTo.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** "Kosongkan": the whole bin goes; the confirmation word is always required (SPEC §6.3). */
export function EmptyTrashDialog({
  count,
  bytes,
  onResolve,
}: {
  count: number
  bytes: number
  onResolve: (ok: boolean) => void
}) {
  const { t, i18n } = useTranslation('trash')
  const lang = i18n.resolvedLanguage ?? 'id'
  const [typed, setTyped] = useState('')
  const [done, setDone] = useState(false)
  const ok = typed.trim() === PERMANENT_CONFIRM_WORD
  const confirm = () => {
    if (!ok || done) return
    setDone(true)
    onResolve(true)
  }
  return (
    <AlertDialog open onOpenChange={(o) => (!o && !done ? onResolve(false) : undefined)}>
      <AlertDialogContent data-testid="empty-trash">
        <div>
          <AlertDialogHeader>
            <AlertDialogTitle className="text-danger">{t('emptyDialog.title')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('emptyDialog.description', {
                count,
                n: formatNumber(count, lang),
                size: formatBytes(bytes, lang),
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className="mt-4 space-y-1">
            <Label htmlFor="empty-trash-word">
              {t('emptyDialog.typeWord', { word: PERMANENT_CONFIRM_WORD })}
            </Label>
            <Input
              id="empty-trash-word"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  confirm()
                }
              }}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              data-testid="empty-trash-input"
            />
          </div>
          <AlertDialogFooter className="mt-4">
            <AlertDialogCancel type="button" onClick={() => onResolve(false)}>
              {t('emptyDialog.cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              type="button"
              disabled={!ok}
              onClick={confirm}
              data-testid="empty-trash-ok"
            >
              {t('emptyDialog.confirm')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  )
}
