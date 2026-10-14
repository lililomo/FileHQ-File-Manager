import { KeyRound } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
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

export interface PasswordPromptProps {
  archiveName: string
  /** Shown under the field after a wrong attempt. */
  error?: string | null | undefined
  busy?: boolean | undefined
  onSubmit: (password: string) => void
  onCancel: () => void
}

/** Password prompt of the archive browser (SPEC §8.11): the value lives in
 *  component state only, is sent in a request header and is remembered by
 *  the server as a short-lived session id. */
export function PasswordPrompt({
  archiveName,
  error,
  busy,
  onSubmit,
  onCancel,
}: PasswordPromptProps) {
  const { t } = useTranslation(['archive', 'fileops'])
  const [value, setValue] = useState('')
  return (
    <Dialog open onOpenChange={(o) => (!o ? onCancel() : undefined)}>
      <DialogContent className="sm:max-w-sm" data-testid="archive-password">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <KeyRound aria-hidden="true" className="size-4" />
            {t('archive:browser.password.title')}
          </DialogTitle>
          <DialogDescription className="truncate">
            {t('archive:browser.password.description', { name: archiveName })}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (value) onSubmit(value)
          }}
        >
          <div className="space-y-1">
            <Label htmlFor="archive-password-input">{t('archive:extract.password')}</Label>
            <Input
              id="archive-password-input"
              type="password"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              autoComplete="off"
              autoFocus
              aria-invalid={error ? true : undefined}
              data-testid="archive-password-input"
            />
            {error ? (
              <p className="text-xs text-danger" role="alert" data-testid="archive-password-error">
                {error}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel}>
              {t('fileops:cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={!value || !!busy}
              data-testid="archive-password-submit"
            >
              {t('archive:browser.password.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
