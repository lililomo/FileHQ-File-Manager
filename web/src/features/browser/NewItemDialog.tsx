import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { ApiError } from '@/lib/api'
import { announce } from '@/lib/announce'
import { createFile, mkdir } from './api'

export type NewItemKind = 'folder' | 'file'

export interface NewItemDialogProps {
  kind: NewItemKind | null
  dir: string
  onClose: () => void
}

type LooseT = (key: string, options?: Record<string, unknown>) => string

function errorMessage(t: LooseT, e: unknown): string {
  if (e instanceof ApiError) {
    const reason = typeof e.details.reason === 'string' ? e.details.reason : undefined
    if (reason) {
      const msg = t(`errors:reason.${reason}`, { defaultValue: '' })
      if (msg) return msg
    }
    const msg = t(`errors:${e.code}`, { defaultValue: '' })
    return msg || e.message
  }
  return e instanceof Error ? e.message : String(e)
}

/** Minimal "Folder baru / File baru" dialog (inline rename arrives in 9b). */
export function NewItemDialog({ kind, dir, onClose }: NewItemDialogProps) {
  const { t } = useTranslation(['files', 'common', 'errors'])
  return (
    <Dialog open={kind !== null} onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent className="sm:max-w-sm">
        {kind ? <NewItemForm key={kind} kind={kind} dir={dir} onClose={onClose} t={t} /> : null}
      </DialogContent>
    </Dialog>
  )
}

function NewItemForm({
  kind,
  dir,
  onClose,
  t,
}: {
  kind: NewItemKind
  dir: string
  onClose: () => void
  t: ReturnType<typeof useTranslation<['files', 'common', 'errors']>>['t']
}) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const mutation = useMutation({
    mutationFn: (n: string) => (kind === 'folder' ? mkdir(dir, n) : createFile(dir, n)),
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ['list', dir] })
      toast.success(t('files:dialog.created', { name: res.item.name }))
      if (res.warnings.includes('windows_incompatible')) {
        toast.warning(t('files:dialog.windowsIncompatible'))
      }
      announce(t('files:dialog.created', { name: res.item.name }))
      onClose()
    },
    onError: (e) => setError(errorMessage(t as unknown as LooseT, e)),
  })
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        const n = name.trim()
        if (!n) return
        mutation.mutate(n)
      }}
    >
      <DialogHeader>
        <DialogTitle>
          {kind === 'file' ? t('files:dialog.newFileTitle') : t('files:dialog.newFolderTitle')}
        </DialogTitle>
        <DialogDescription className="font-mono text-xs">{dir}</DialogDescription>
      </DialogHeader>
      <div className="mt-4 space-y-2">
        <Label htmlFor="new-item-name">{t('files:dialog.nameLabel')}</Label>
        <Input
          id="new-item-name"
          value={name}
          onChange={(e) => {
            setName(e.target.value)
            setError(null)
          }}
          placeholder={
            kind === 'file'
              ? t('files:dialog.filePlaceholder')
              : t('files:dialog.folderPlaceholder')
          }
          autoFocus
          autoComplete="off"
          spellCheck={false}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? 'new-item-error' : undefined}
          data-testid="new-item-name"
        />
        {error ? (
          <p id="new-item-error" role="alert" className="text-xs text-danger">
            {error}
          </p>
        ) : null}
      </div>
      <DialogFooter className="mt-4">
        <Button type="button" variant="outline" onClick={onClose}>
          {t('common:actions.cancel')}
        </Button>
        <Button
          type="submit"
          variant="primary"
          disabled={!name.trim() || mutation.isPending}
          data-testid="new-item-submit"
        >
          {t('files:dialog.create')}
        </Button>
      </DialogFooter>
    </form>
  )
}
