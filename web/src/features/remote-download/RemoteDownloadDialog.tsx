import { Globe, Search } from 'lucide-react'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { normalizeTypedPath, ROOT } from '@/features/browser/path'
import type { ConflictPolicy } from '@/features/file-ops/api'
import { FolderPicker } from '@/features/file-ops/FolderPicker'
import { errorMessage } from '@/features/file-ops/useFileOps'
import { taskLabel } from '@/features/tasks/taskLabel'
import { announce } from '@/lib/announce'
import { formatBytes, formatNumber } from '@/lib/format'
import { useTasksStore } from '@/stores/tasks'
import {
  createRemoteDownloads,
  MAX_REMOTE_URLS,
  parseUrlLines,
  probeRemote,
  type ProbeResult,
} from './api'

export interface RemoteDownloadDialogProps {
  dir: string
  onClose: () => void
  onCloseAutoFocus?: (e: Event) => void
}

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** "Remote download" (SPEC §8.10): URLs one per line, destination folder,
 *  file name for a single URL, conflict policy and "Periksa" (probe). Each
 *  URL becomes a task shown in the task panel. */
export function RemoteDownloadDialog({
  dir,
  onClose,
  onCloseAutoFocus,
}: RemoteDownloadDialogProps) {
  const { t, i18n } = useTranslation(['remote', 'fileops', 'errors', 'tasks'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const [text, setText] = useState('')
  const [dest, setDest] = useState(dir)
  const [typed, setTyped] = useState(dir)
  const [typedError, setTypedError] = useState<string | null>(null)
  const [filename, setFilename] = useState('')
  const [policy, setPolicy] = useState<ConflictPolicy>('rename')
  const [probe, setProbe] = useState<ProbeResult | null>(null)
  const [probing, setProbing] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const parsed = parseUrlLines(text)
  const single = parsed.urls.length === 1 && parsed.problems.length === 0
  const problem = parsed.problems.find((p) => p !== 'empty') ?? null
  const lineCount = text.split(/\r?\n/).filter((l) => l.trim()).length

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

  const runProbe = async () => {
    if (!single) return
    setProbing(true)
    setError(null)
    try {
      const res = await probeRemote(parsed.urls[0]!)
      setProbe(res)
      if (!filename && res.filename) setFilename(res.filename)
    } catch (e) {
      setProbe(null)
      setError(errorMessage(lt, e))
    } finally {
      setProbing(false)
    }
  }

  const submit = async () => {
    const p = applyTyped()
    if (!p || parsed.problems.length > 0 || submitting) return
    setSubmitting(true)
    setError(null)
    try {
      const res = await createRemoteDownloads({
        urls: parsed.urls,
        path: p,
        filename: single && filename.trim() ? filename.trim() : undefined,
        onConflict: policy,
      })
      const store = useTasksStore.getState()
      for (const task of res.tasks) store.upsert(task)
      store.setPanel(true)
      const first = res.tasks[0]
      const msg =
        res.tasks.length === 1 && first
          ? t('tasks:toast.queued', { title: taskLabel(first, lt, lang) })
          : t('remote:queuedMany', {
              count: res.tasks.length,
              n: formatNumber(res.tasks.length, lang),
            })
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
        data-testid="remote-dialog"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Globe aria-hidden="true" className="size-4" />
            {t('remote:title')}
          </DialogTitle>
          <DialogDescription>{t('remote:description', { max: MAX_REMOTE_URLS })}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <div className="space-y-1">
            <Label htmlFor="remote-urls">{t('remote:urls')}</Label>
            <Textarea
              id="remote-urls"
              value={text}
              onChange={(e) => {
                setText(e.target.value)
                setProbe(null)
                setError(null)
              }}
              placeholder="https://example.com/file.zip"
              rows={4}
              className="font-mono text-xs"
              spellCheck={false}
              autoComplete="off"
              autoFocus
              aria-invalid={problem ? true : undefined}
              aria-describedby="remote-urls-hint"
              data-testid="remote-urls"
            />
            <p id="remote-urls-hint" className="text-xs text-text-muted">
              {problem ? (
                <span className="text-danger" role="alert" data-testid="remote-urls-error">
                  {t(`remote:problem.${problem}`, { max: MAX_REMOTE_URLS })}
                </span>
              ) : (
                t('remote:count', { count: lineCount, n: formatNumber(lineCount, lang) })
              )}
            </p>
          </div>
          <div className="space-y-1">
            <Label htmlFor="remote-dest">{t('remote:destination')}</Label>
            <Input
              id="remote-dest"
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
              aria-describedby={typedError ? 'remote-dest-error' : undefined}
              data-testid="remote-dest"
            />
            {typedError ? (
              <p className="text-xs text-danger" role="alert" id="remote-dest-error">
                {typedError}
              </p>
            ) : null}
          </div>
          <FolderPicker value={dest} onChange={choose} className="max-h-40" />
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="remote-filename">{t('remote:filename')}</Label>
              <div className="flex gap-1">
                <Input
                  id="remote-filename"
                  value={filename}
                  onChange={(e) => setFilename(e.target.value)}
                  disabled={!single}
                  placeholder={single ? t('remote:filenameAuto') : t('remote:filenameSingle')}
                  spellCheck={false}
                  autoComplete="off"
                  data-testid="remote-filename"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-9 shrink-0 gap-1"
                  disabled={!single || probing}
                  onClick={() => void runProbe()}
                  aria-label={t('remote:probe')}
                  data-testid="remote-probe"
                >
                  <Search aria-hidden="true" />
                  {t('remote:probe')}
                </Button>
              </div>
              {probe ? (
                <p className="text-xs text-text-muted" data-testid="remote-probe-result">
                  {probe.size !== null
                    ? t('remote:probeSize', { size: formatBytes(probe.size, lang) })
                    : t('remote:probeUnknownSize')}
                  {probe.acceptsRanges ? ` · ${t('remote:probeResumable')}` : ''}
                </p>
              ) : null}
            </div>
            <div className="space-y-1">
              <Label htmlFor="remote-policy">{t('remote:policy')}</Label>
              <Select value={policy} onValueChange={(v) => setPolicy(v as ConflictPolicy)}>
                <SelectTrigger id="remote-policy" data-testid="remote-policy">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="rename">{t('fileops:conflict.rename')}</SelectItem>
                  <SelectItem value="overwrite">{t('fileops:conflict.overwrite')}</SelectItem>
                  <SelectItem value="skip">{t('fileops:conflict.skip')}</SelectItem>
                  <SelectItem value="fail">{t('remote:policyFail')}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          {error ? (
            <p className="text-xs text-danger" role="alert" data-testid="remote-error">
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
              disabled={parsed.problems.length > 0 || submitting}
              data-testid="remote-submit"
            >
              {t('remote:submit', { count: parsed.urls.length })}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
