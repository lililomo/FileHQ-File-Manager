import { Archive } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { normalizeTypedPath, ROOT } from '@/features/browser/path'
import { FolderPicker } from '@/features/file-ops/FolderPicker'
import { errorMessage } from '@/features/file-ops/useFileOps'
import { taskLabel } from '@/features/tasks/taskLabel'
import { announce } from '@/lib/announce'
import { formatNumber } from '@/lib/format'
import { useTasksStore } from '@/stores/tasks'
import {
  compressItems,
  defaultArchiveName,
  getArchiveCapabilities,
  type ArchiveCapability,
  type ArchiveFormat,
  type CompressLevel,
} from './api'

export interface CompressDialogProps {
  paths: string[]
  dir: string
  onClose: () => void
  onCloseAutoFocus?: (e: Event) => void
}

type LooseT = (key: string, options?: Record<string, unknown>) => string

const LEVELS: CompressLevel[] = ['store', 'fast', 'normal', 'max']

/** "Compress…" (SPEC §8.11): format from the server capabilities, default
 *  name, destination folder, level, optional password (zip AES-256 / 7z),
 *  encrypted names and split volumes for 7z, "delete sources" to the
 *  Recycle Bin. The work runs as a task shown in the task panel. */
export function CompressDialog({ paths, dir, onClose, onCloseAutoFocus }: CompressDialogProps) {
  const { t, i18n } = useTranslation(['archive', 'fileops', 'errors', 'tasks'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const [caps, setCaps] = useState<ArchiveCapability[] | null>(null)
  const [capsError, setCapsError] = useState<string | null>(null)
  const [format, setFormat] = useState<ArchiveFormat>('zip')
  const [name, setName] = useState('')
  const [nameTouched, setNameTouched] = useState(false)
  const [dest, setDest] = useState(dir)
  const [typed, setTyped] = useState(dir)
  const [typedError, setTypedError] = useState<string | null>(null)
  const [level, setLevel] = useState<CompressLevel>('normal')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [encryptNames, setEncryptNames] = useState(false)
  const [split, setSplit] = useState('')
  const [deleteSource, setDeleteSource] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    getArchiveCapabilities().then(
      (c) => {
        if (alive) setCaps(c.formats.filter((f) => f.canWrite))
      },
      (e: unknown) => {
        if (alive) setCapsError(errorMessage(lt, e))
      },
    )
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const cap = useMemo(() => caps?.find((c) => c.format === format) ?? null, [caps, format])
  const extension = cap?.extension ?? format
  const singleFile = cap?.singleFile ?? false
  const effectiveName =
    nameTouched && name.trim() ? name.trim() : defaultArchiveName(paths, extension)
  const passwordMismatch = password !== '' && password !== confirm
  const singleFileBlocked = singleFile && paths.length !== 1
  const splitNumber = split.trim() === '' ? 0 : Number(split)
  const splitInvalid = split.trim() !== '' && (!Number.isInteger(splitNumber) || splitNumber < 1)

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
    const p = applyTyped()
    if (!p || submitting || passwordMismatch || singleFileBlocked || splitInvalid) return
    setSubmitting(true)
    setError(null)
    try {
      const res = await compressItems({
        paths,
        format,
        name: effectiveName,
        destPath: p,
        level,
        password: password || undefined,
        encryptNames: password && cap?.encryptNames ? encryptNames : undefined,
        deleteSource: deleteSource || undefined,
        splitVolumeMb: cap?.splitVolume && splitNumber > 0 ? splitNumber : undefined,
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
        data-testid="compress-dialog"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Archive aria-hidden="true" className="size-4" />
            {t('archive:compress.title')}
          </DialogTitle>
          <DialogDescription>
            {t('archive:compress.description', {
              count: paths.length,
              n: formatNumber(paths.length, lang),
            })}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="compress-format">{t('archive:compress.format')}</Label>
              <Select value={format} onValueChange={(v) => setFormat(v as ArchiveFormat)}>
                <SelectTrigger id="compress-format" data-testid="compress-format">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(caps ?? []).map((c) => (
                    <SelectItem key={c.format} value={c.format}>
                      {c.format}
                      {c.singleFile ? ` · ${t('archive:compress.singleFile')}` : ''}
                    </SelectItem>
                  ))}
                  {!caps ? <SelectItem value={format}>{format}</SelectItem> : null}
                </SelectContent>
              </Select>
              {capsError ? (
                <p className="text-xs text-danger" role="alert">
                  {capsError}
                </p>
              ) : null}
            </div>
            <div className="space-y-1">
              <Label htmlFor="compress-level">{t('archive:compress.level')}</Label>
              <Select
                value={level}
                onValueChange={(v) => setLevel(v as CompressLevel)}
                disabled={cap ? !cap.levels : false}
              >
                <SelectTrigger id="compress-level" data-testid="compress-level">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {LEVELS.map((l) => (
                    <SelectItem key={l} value={l}>
                      {t(`archive:level.${l}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="space-y-1">
            <Label htmlFor="compress-name">{t('archive:compress.name')}</Label>
            <Input
              id="compress-name"
              value={nameTouched ? name : effectiveName}
              onChange={(e) => {
                setNameTouched(true)
                setName(e.target.value)
              }}
              spellCheck={false}
              autoComplete="off"
              data-testid="compress-name"
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="compress-dest">{t('archive:compress.destination')}</Label>
            <Input
              id="compress-dest"
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
              aria-describedby={typedError ? 'compress-dest-error' : undefined}
              data-testid="compress-dest"
            />
            {typedError ? (
              <p className="text-xs text-danger" role="alert" id="compress-dest-error">
                {typedError}
              </p>
            ) : null}
          </div>
          <FolderPicker value={dest} onChange={choose} className="max-h-32" />
          {cap?.passwordWrite ? (
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label htmlFor="compress-password">{t('archive:compress.password')}</Label>
                <Input
                  id="compress-password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                  data-testid="compress-password"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="compress-confirm">{t('archive:compress.confirm')}</Label>
                <Input
                  id="compress-confirm"
                  type="password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  autoComplete="new-password"
                  aria-invalid={passwordMismatch ? true : undefined}
                  aria-describedby={passwordMismatch ? 'compress-confirm-error' : undefined}
                  data-testid="compress-confirm"
                />
                {passwordMismatch ? (
                  <p className="text-xs text-danger" role="alert" id="compress-confirm-error">
                    {t('archive:compress.mismatch')}
                  </p>
                ) : null}
              </div>
              {password ? (
                <p className="text-xs text-text-muted sm:col-span-2">
                  {format === '7z' ? t('archive:compress.hint7z') : t('archive:compress.hintZip')}
                </p>
              ) : null}
            </div>
          ) : null}
          {cap?.encryptNames && password ? (
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={encryptNames}
                onCheckedChange={(v) => setEncryptNames(v === true)}
                data-testid="compress-encrypt-names"
              />
              {t('archive:compress.encryptNames')}
            </label>
          ) : null}
          {cap?.splitVolume ? (
            <div className="space-y-1">
              <Label htmlFor="compress-split">{t('archive:compress.split')}</Label>
              <Input
                id="compress-split"
                inputMode="numeric"
                value={split}
                onChange={(e) => setSplit(e.target.value)}
                placeholder={t('archive:compress.splitNone')}
                aria-invalid={splitInvalid ? true : undefined}
                aria-describedby={splitInvalid ? 'compress-split-error' : undefined}
                data-testid="compress-split"
              />
            </div>
          ) : null}
          <label className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={deleteSource}
              onCheckedChange={(v) => setDeleteSource(v === true)}
              data-testid="compress-delete-source"
            />
            {t('archive:compress.deleteSource')}
          </label>
          {singleFileBlocked ? (
            <p className="text-xs text-danger" role="alert" id="compress-split-error">
              {t('archive:compress.singleFileOnly')}
            </p>
          ) : null}
          {error ? (
            <p className="text-xs text-danger" role="alert" data-testid="compress-error">
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
              disabled={
                submitting || passwordMismatch || singleFileBlocked || splitInvalid || !!capsError
              }
              data-testid="compress-submit"
            >
              {t('archive:compress.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
