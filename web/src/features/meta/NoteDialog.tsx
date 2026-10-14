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
import { Textarea } from '@/components/ui/textarea'
import type { Item } from '@/features/browser/api'
import { cn } from '@/lib/utils'
import { MAX_NOTE_CHARS } from './api'

/** "Catatan…" dialog (context menu / Properties): plain text ≤ 500 chars. */
export function NoteDialog({
  item,
  onClose,
  onSave,
}: {
  item: Item | null
  onClose: () => void
  onSave: (path: string, note: string) => Promise<boolean>
}) {
  const { t } = useTranslation('meta')
  return (
    <Dialog open={!!item} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md" data-testid="note-dialog">
        <DialogHeader>
          <DialogTitle>{t('note.title')}</DialogTitle>
          <DialogDescription className="font-mono text-xs">{item?.path}</DialogDescription>
        </DialogHeader>
        {item ? <NoteForm key={item.path} item={item} onClose={onClose} onSave={onSave} /> : null}
      </DialogContent>
    </Dialog>
  )
}

function NoteForm({
  item,
  onClose,
  onSave,
}: {
  item: Item
  onClose: () => void
  onSave: (path: string, note: string) => Promise<boolean>
}) {
  const { t } = useTranslation(['meta', 'common'])
  const [value, setValue] = useState(item.note ?? '')
  const [busy, setBusy] = useState(false)
  const tooLong = value.length > MAX_NOTE_CHARS
  const submit = async () => {
    if (tooLong) return
    setBusy(true)
    const ok = await onSave(item.path, value)
    setBusy(false)
    if (ok) onClose()
  }
  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <Textarea
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder={t('meta:note.placeholder')}
        aria-label={t('meta:note.title')}
        aria-invalid={tooLong || undefined}
        rows={4}
        autoFocus
        data-testid="note-dialog-input"
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
            e.preventDefault()
            void submit()
          }
        }}
      />
      <div
        className={cn('text-xs tabular', tooLong ? 'text-danger' : 'text-text-muted')}
        aria-live="polite"
      >
        {tooLong
          ? t('meta:note.tooLong', { max: MAX_NOTE_CHARS })
          : t('meta:note.counter', { n: value.length, max: MAX_NOTE_CHARS })}
      </div>
      <DialogFooter>
        {item.note ? (
          <Button
            type="button"
            variant="ghost"
            onClick={() => setValue('')}
            data-testid="note-dialog-clear"
          >
            {t('meta:note.clear')}
          </Button>
        ) : null}
        <Button type="button" variant="outline" onClick={onClose}>
          {t('common:actions.cancel')}
        </Button>
        <Button
          type="submit"
          variant="primary"
          disabled={busy || tooLong}
          data-testid="note-dialog-save"
        >
          {t('meta:note.save')}
        </Button>
      </DialogFooter>
    </form>
  )
}
