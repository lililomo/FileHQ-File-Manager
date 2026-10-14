import { useState } from 'react'
import { useTranslation } from 'react-i18next'
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
import type { Item } from '@/features/browser/api'
import { formatNumber } from '@/lib/format'
import type { ChmodInput } from './api'
import { BITS, WHO, bitsToOctal, octalToBits, type Bit, type Who } from './permBits'

export interface PermissionDialogProps {
  items: Item[]
  onClose: () => void
  onSubmit: (input: ChmodInput) => Promise<unknown>
  onCloseAutoFocus?: (e: Event) => void
}

/** Permission dialog (PROMPT 9b task 9): rwx matrix × owner/group/public,
 *  octal field kept in sync, optional recursion for folders. */
export function PermissionDialog({
  items,
  onClose,
  onSubmit,
  onCloseAutoFocus,
}: PermissionDialogProps) {
  const { t, i18n } = useTranslation('fileops')
  const lang = i18n.resolvedLanguage ?? 'id'
  const first = items[0]
  const [octal, setOctal] = useState(() => {
    const raw = first?.modeOctal ?? '0644'
    return (
      '0' +
      raw
        .replace(/[^0-7]/g, '')
        .slice(-3)
        .padStart(3, '0')
    )
  })
  const [recursive, setRecursive] = useState(false)
  const [busy, setBusy] = useState(false)
  const bits = octalToBits(octal)
  const hasDir = items.some((it) => it.isDir)
  const valid = /^0?[0-7]{3}$/.test(octal)
  const toggle = (who: Who, bit: Bit) => {
    const next = { ...bits, [who]: { ...bits[who], [bit]: !bits[who][bit] } }
    setOctal(bitsToOctal(next))
  }
  const submit = async () => {
    if (!valid) return
    setBusy(true)
    try {
      await onSubmit({ paths: items.map((it) => it.path), mode: octal.padStart(4, '0'), recursive })
      onClose()
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent
        className="sm:max-w-sm"
        data-testid="permission-dialog"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <DialogHeader>
            <DialogTitle>{t('perm.title')}</DialogTitle>
            <DialogDescription>
              {items.length === 1
                ? first?.path
                : t('perm.items', { count: items.length, n: formatNumber(items.length, lang) })}
            </DialogDescription>
          </DialogHeader>
          <table className="mt-4 w-full text-sm">
            <thead>
              <tr className="text-xs text-text-muted">
                <th className="py-1 text-left font-medium"></th>
                {BITS.map((b) => (
                  <th key={b} className="py-1 text-center font-medium">
                    {t(`perm.bit.${b}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {WHO.map((who) => (
                <tr key={who} className="border-t border-border/60">
                  <th scope="row" className="py-1.5 text-left font-normal">
                    {t(`perm.who.${who}`)}
                  </th>
                  {BITS.map((b) => (
                    <td key={b} className="py-1.5 text-center">
                      <Checkbox
                        checked={bits[who][b]}
                        onCheckedChange={() => toggle(who, b)}
                        aria-label={`${t(`perm.who.${who}`)} ${t(`perm.bit.${b}`)}`}
                        data-testid={`perm-${who}-${b}`}
                      />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          <div className="mt-3 flex items-center gap-3">
            <Label htmlFor="perm-octal">{t('perm.octal')}</Label>
            <Input
              id="perm-octal"
              value={octal}
              onChange={(e) => setOctal(e.target.value.replace(/[^0-7]/g, '').slice(0, 4))}
              className="w-24 font-mono"
              inputMode="numeric"
              aria-invalid={valid ? undefined : true}
              data-testid="perm-octal"
            />
          </div>
          {hasDir ? (
            <label className="mt-3 flex items-center gap-2 text-sm">
              <Checkbox
                checked={recursive}
                onCheckedChange={(v) => setRecursive(v === true)}
                data-testid="perm-recursive"
              />
              {t('perm.recursive')}
            </label>
          ) : null}
          <p className="mt-2 text-xs text-text-muted">{t('perm.note')}</p>
          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" onClick={onClose}>
              {t('cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={!valid || busy}
              data-testid="perm-submit"
            >
              {t('perm.apply')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
