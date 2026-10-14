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
import { Checkbox } from '@/components/ui/checkbox'
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
import { baseName } from '@/features/browser/path'
import { formatNumber } from '@/lib/format'
import { usePrefsStore } from '@/stores/prefs'

export const PERMANENT_CONFIRM_WORD = 'HAPUS'
export const PERMANENT_CONFIRM_THRESHOLD = 50

/** Delete → Recycle Bin confirmation (preference `fileops.confirm_delete`). */
export function DeleteConfirmDialog({
  paths,
  onResolve,
  onCloseAutoFocus,
}: {
  paths: string[]
  onResolve: (ok: boolean) => void
  onCloseAutoFocus?: (e: Event) => void
}) {
  const { t, i18n } = useTranslation('fileops')
  const lang = i18n.resolvedLanguage ?? 'id'
  const [dontAsk, setDontAsk] = useState(false)
  const setPref = usePrefsStore((s) => s.set)
  const n = paths.length
  return (
    <AlertDialog open onOpenChange={(o) => (!o ? onResolve(false) : undefined)}>
      <AlertDialogContent data-testid="delete-confirm" onCloseAutoFocus={onCloseAutoFocus}>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {n === 1
              ? t('delete.titleOne', { name: baseName(paths[0]!) })
              : t('delete.titleMany', { count: n, n: formatNumber(n, lang) })}
          </AlertDialogTitle>
          <AlertDialogDescription>{t('delete.description')}</AlertDialogDescription>
        </AlertDialogHeader>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={dontAsk}
            onCheckedChange={(v) => setDontAsk(v === true)}
            data-testid="delete-dont-ask"
          />
          {t('delete.dontAsk')}
        </label>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => onResolve(false)}>{t('cancel')}</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              if (dontAsk) setPref('fileops.confirm_delete', false)
              onResolve(true)
            }}
            data-testid="delete-confirm-ok"
          >
            {t('delete.confirm')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** Shift+Delete (SPEC §8.5 "Hapus permanen"): > 50 items require typing HAPUS. */
export function DeletePermanentDialog({
  paths,
  onResolve,
  onCloseAutoFocus,
}: {
  paths: string[]
  onResolve: (ok: boolean) => void
  onCloseAutoFocus?: (e: Event) => void
}) {
  const { t, i18n } = useTranslation('fileops')
  const lang = i18n.resolvedLanguage ?? 'id'
  const [typed, setTyped] = useState('')
  const n = paths.length
  const needsWord = n > PERMANENT_CONFIRM_THRESHOLD
  const ok = !needsWord || typed.trim() === PERMANENT_CONFIRM_WORD
  return (
    <Dialog open onOpenChange={(o) => (!o ? onResolve(false) : undefined)}>
      <DialogContent
        className="sm:max-w-md"
        data-testid="delete-permanent"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (ok) onResolve(true)
          }}
        >
          <DialogHeader>
            <DialogTitle className="text-danger">
              {n === 1
                ? t('permanent.titleOne', { name: baseName(paths[0]!) })
                : t('permanent.titleMany', { count: n, n: formatNumber(n, lang) })}
            </DialogTitle>
            <DialogDescription>{t('permanent.description')}</DialogDescription>
          </DialogHeader>
          {needsWord ? (
            <div className="mt-4 space-y-1">
              <Label htmlFor="delete-permanent-word">
                {t('permanent.typeWord', { word: PERMANENT_CONFIRM_WORD })}
              </Label>
              <Input
                id="delete-permanent-word"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                autoFocus
                autoComplete="off"
                spellCheck={false}
                data-testid="delete-permanent-input"
              />
            </div>
          ) : null}
          <p
            className="mt-3 text-xs text-text-muted"
            role="note"
            data-testid="delete-permanent-note"
          >
            {t('permanent.fallbackNote')}
          </p>
          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" onClick={() => onResolve(false)}>
              {t('cancel')}
            </Button>
            <Button type="submit" variant="danger" disabled={!ok} data-testid="delete-permanent-ok">
              {t('permanent.confirm')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
