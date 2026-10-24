import { PackageOpen } from 'lucide-react'
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
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { normalizeTypedPath, ROOT } from '@/features/browser/path'
import type { ConflictPolicy } from '@/features/file-ops/api'
import { FolderPicker } from '@/features/file-ops/FolderPicker'

export interface ExtractEntriesDialogProps {
  archiveName: string
  /** folder the archive lives in */
  dir: string
  /** number of selected members (0 = whole archive) */
  count: number
  error?: string | null | undefined
  busy?: boolean | undefined
  onSubmit: (dest: string, policy: ConflictPolicy) => void
  onClose: () => void
}

type DestMode = 'new' | 'here' | 'choose'

/** "Extract selected to…" / "Extract all" of the archive browser: the
 *  destination choices of the Step 14a dialog, no password field (the
 *  browser already holds it). */
export function ExtractEntriesDialog({
  archiveName,
  dir,
  count,
  error,
  busy,
  onSubmit,
  onClose,
}: ExtractEntriesDialogProps) {
  const { t } = useTranslation(['archive', 'fileops'])
  const [mode, setMode] = useState<DestMode>('new')
  const [dest, setDest] = useState(dir)
  const [typed, setTyped] = useState(dir)
  const [typedError, setTypedError] = useState<string | null>(null)
  const [policy, setPolicy] = useState<ConflictPolicy>('rename')
  const stem = archiveName.replace(/\.(tar\.(gz|bz2|xz|zst)|t(gz|bz2|xz|zst)|[^.]+)$/i, '')

  const choose = (p: string) => {
    setDest(p)
    setTyped(p)
    setTypedError(null)
  }
  const submit = () => {
    let target: string = mode
    if (mode === 'choose') {
      const r = normalizeTypedPath(typed, ROOT)
      if (!r.ok) {
        setTypedError(t('fileops:moveCopy.invalidPath'))
        return
      }
      target = r.path
    }
    onSubmit(target, policy)
  }

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent className="sm:max-w-lg" data-testid="extract-entries-dialog">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <PackageOpen aria-hidden="true" className="size-4" />
            {count > 0
              ? t('archive:browser.extractSelected.title', { count })
              : t('archive:browser.extractAll.title')}
          </DialogTitle>
          <DialogDescription className="truncate">
            {t('archive:extract.description', { name: archiveName })}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">{t('archive:extract.destination')}</legend>
            <RadioGroup value={mode} onValueChange={(v) => setMode(v as DestMode)}>
              <label className="flex items-center gap-2 text-sm">
                <RadioGroupItem value="new" data-testid="extract-entries-new" />
                {t('archive:extract.destNew', { name: stem })}
              </label>
              <label className="flex items-center gap-2 text-sm">
                <RadioGroupItem value="here" data-testid="extract-entries-here" />
                {t('archive:extract.destHere', { dir })}
              </label>
              <label className="flex items-center gap-2 text-sm">
                <RadioGroupItem value="choose" data-testid="extract-entries-choose" />
                {t('archive:extract.destChoose')}
              </label>
            </RadioGroup>
          </fieldset>
          {mode === 'choose' ? (
            <div className="space-y-2">
              <Input
                aria-label={t('archive:extract.destChoose')}
                value={typed}
                onChange={(e) => {
                  setTyped(e.target.value)
                  setTypedError(null)
                }}
                className="font-mono text-xs"
                spellCheck={false}
                autoComplete="off"
                aria-invalid={typedError ? true : undefined}
                aria-describedby={typedError ? 'extract-entries-dest-error' : undefined}
                data-testid="extract-entries-dest"
              />
              {typedError ? (
                <p className="text-xs text-danger" role="alert" id="extract-entries-dest-error">
                  {typedError}
                </p>
              ) : null}
              <FolderPicker value={dest} onChange={choose} className="max-h-32" />
            </div>
          ) : null}
          <div className="space-y-1">
            <Label htmlFor="extract-entries-policy">{t('archive:extract.policy')}</Label>
            <Select value={policy} onValueChange={(v) => setPolicy(v as ConflictPolicy)}>
              <SelectTrigger id="extract-entries-policy" data-testid="extract-entries-policy">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="rename">{t('fileops:conflict.rename')}</SelectItem>
                <SelectItem value="overwrite">{t('fileops:conflict.overwrite')}</SelectItem>
                <SelectItem value="skip">{t('fileops:conflict.skip')}</SelectItem>
                <SelectItem value="fail">{t('archive:extract.policyFail')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {error ? (
            <p className="text-xs text-danger" role="alert" data-testid="extract-entries-error">
              {error}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('fileops:cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={!!busy}
              data-testid="extract-entries-submit"
            >
              {t('archive:extract.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
