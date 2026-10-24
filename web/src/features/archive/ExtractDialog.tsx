import { PackageOpen } from 'lucide-react'
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
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { Item } from '@/features/browser/api'
import { normalizeTypedPath, ROOT } from '@/features/browser/path'
import type { ConflictPolicy } from '@/features/file-ops/api'
import { FolderPicker } from '@/features/file-ops/FolderPicker'
import { errorMessage } from '@/features/file-ops/useFileOps'
import { taskLabel } from '@/features/tasks/taskLabel'
import { announce } from '@/lib/announce'
import { useTasksStore } from '@/stores/tasks'
import { archiveStem, extractArchive, NAME_ENCODINGS, type NameEncoding } from './api'

export interface ExtractDialogProps {
  archive: Item
  dir: string
  onClose: () => void
  onCloseAutoFocus?: (e: Event) => void
  /** Pre-filled when the previous attempt failed with ARCHIVE_PASSWORD_*; */
  initialPassword?: string | undefined
  passwordError?: string | undefined
}

type LooseT = (key: string, options?: Record<string, unknown>) => string
type DestMode = 'new' | 'here' | 'choose'

/** "Extract" (SPEC §8.11): destination (new folder named after the
 *  archive by default, next to it, or a chosen folder), password, name
 *  encoding and conflict policy; runs as a task. */
export function ExtractDialog({
  archive,
  dir,
  onClose,
  onCloseAutoFocus,
  initialPassword,
  passwordError,
}: ExtractDialogProps) {
  const { t, i18n } = useTranslation(['archive', 'fileops', 'errors', 'tasks'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const [mode, setMode] = useState<DestMode>('new')
  const [dest, setDest] = useState(dir)
  const [typed, setTyped] = useState(dir)
  const [typedError, setTypedError] = useState<string | null>(null)
  const [password, setPassword] = useState(initialPassword ?? '')
  const [encoding, setEncoding] = useState<NameEncoding>('auto')
  const [policy, setPolicy] = useState<ConflictPolicy>('rename')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(passwordError ?? null)

  const stem = archiveStem(archive.name)
  const choose = (p: string) => {
    setDest(p)
    setTyped(p)
    setTypedError(null)
  }
  const applyTyped = (): string | null => {
    const r = normalizeTypedPath(typed, ROOT)
    if (!r.ok) {
      setTypedError(t('fileops:moveCopy.invalidPath'))
      return null
    }
    choose(r.path)
    return r.path
  }

  const submit = async () => {
    if (submitting) return
    let target = mode
    let chosen = ''
    if (mode === 'choose') {
      const p = applyTyped()
      if (!p) return
      chosen = p
      target = 'choose'
    }
    setSubmitting(true)
    setError(null)
    try {
      const res = await extractArchive({
        path: archive.path,
        dest: target === 'choose' ? chosen : target,
        password: password || undefined,
        encoding,
        onConflict: policy,
      })
      const store = useTasksStore.getState()
      store.upsert(res.task)
      store.setPanel(true)
      const msg = t('tasks:toast.queued', { title: taskLabel(res.task, lt, lang) })
      toast.info(msg)
      announce(msg)
      onClose()
    } catch (e) {
      setError(errorMessage(lt, e))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent
        className="sm:max-w-lg"
        data-testid="extract-dialog"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <PackageOpen aria-hidden="true" className="size-4" />
            {t('archive:extract.title')}
          </DialogTitle>
          <DialogDescription className="truncate">
            {t('archive:extract.description', { name: archive.name })}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">{t('archive:extract.destination')}</legend>
            <RadioGroup value={mode} onValueChange={(v) => setMode(v as DestMode)}>
              <label className="flex items-center gap-2 text-sm">
                <RadioGroupItem value="new" data-testid="extract-dest-new" />
                {t('archive:extract.destNew', { name: stem })}
              </label>
              <label className="flex items-center gap-2 text-sm">
                <RadioGroupItem value="here" data-testid="extract-dest-here" />
                {t('archive:extract.destHere', { dir })}
              </label>
              <label className="flex items-center gap-2 text-sm">
                <RadioGroupItem value="choose" data-testid="extract-dest-choose" />
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
                onBlur={() => {
                  if (typed !== dest) applyTyped()
                }}
                className="font-mono text-xs"
                spellCheck={false}
                autoComplete="off"
                aria-invalid={typedError ? true : undefined}
                aria-describedby={typedError ? 'extract-dest-error' : undefined}
                data-testid="extract-dest"
              />
              {typedError ? (
                <p className="text-xs text-danger" role="alert" id="extract-dest-error">
                  {typedError}
                </p>
              ) : null}
              <FolderPicker value={dest} onChange={choose} className="max-h-32" />
            </div>
          ) : null}
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="extract-password">{t('archive:extract.password')}</Label>
              <Input
                id="extract-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="off"
                placeholder={t('archive:extract.passwordHint')}
                data-testid="extract-password"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="extract-encoding">{t('archive:extract.encoding')}</Label>
              <Select value={encoding} onValueChange={(v) => setEncoding(v as NameEncoding)}>
                <SelectTrigger id="extract-encoding" data-testid="extract-encoding">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {NAME_ENCODINGS.map((enc) => (
                    <SelectItem key={enc} value={enc}>
                      {t(`archive:encoding.${enc}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="space-y-1">
            <Label htmlFor="extract-policy">{t('archive:extract.policy')}</Label>
            <Select value={policy} onValueChange={(v) => setPolicy(v as ConflictPolicy)}>
              <SelectTrigger id="extract-policy" data-testid="extract-policy">
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
            <p className="text-xs text-danger" role="alert" data-testid="extract-error">
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
              disabled={submitting}
              data-testid="extract-submit"
            >
              {t('archive:extract.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
