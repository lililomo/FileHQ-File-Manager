import { Replace, Undo2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/features/file-ops/useFileOps'

type LooseT = (key: string, options?: Record<string, unknown>) => string
import { formatDateTimeFull } from '@/lib/format'
import { useTasksStore } from '@/stores/tasks'
import {
  applyReplace,
  previewReplace,
  restoreVersion,
  waitForTask,
  type ContentQuery,
  type ReplaceApplyResult,
  type ReplacePreviewResult,
} from './api'
import { DiffPreview } from './DiffPreview'

type Phase =
  | { kind: 'idle' }
  | { kind: 'previewing' }
  | { kind: 'previewed'; preview: ReplacePreviewResult }
  | { kind: 'applying'; preview: ReplacePreviewResult }
  | { kind: 'done'; result: ReplaceApplyResult; undone?: boolean }

/** Replace in files (SPEC §8.12): replacement input → preview task (diff per
 *  file with checkboxes) → apply task → result with "Batalkan replace"
 *  (restores the pre-image versions in order). */
export function ReplacePanel({
  query,
  onClose,
  onApplied,
}: {
  query: ContentQuery
  onClose: () => void
  onApplied: () => void
}) {
  const { t, i18n } = useTranslation(['search', 'fileops'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const lt = t as unknown as LooseT
  const upsert = useTasksStore((s) => s.upsert)
  const [replacement, setReplacement] = useState('')
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [undoing, setUndoing] = useState(false)
  const abort = useRef<AbortController | null>(null)
  useEffect(() => () => abort.current?.abort(), [])

  const runPreview = async () => {
    abort.current?.abort()
    const ctl = new AbortController()
    abort.current = ctl
    setPhase({ kind: 'previewing' })
    try {
      const { task } = await previewReplace(query, replacement)
      upsert(task)
      const final = await waitForTask(task.id, ctl.signal)
      upsert(final)
      if (final.state !== 'done') {
        toast.error(t('search:replace.failed', { message: final.error?.message ?? final.state }))
        setPhase({ kind: 'idle' })
        return
      }
      const preview = final.result as unknown as ReplacePreviewResult
      const sel: Record<string, boolean> = {}
      for (const f of preview.files) if (!f.skipped) sel[f.path] = true
      setSelected(sel)
      setPhase({ kind: 'previewed', preview })
    } catch (e) {
      if ((e as { name?: string }).name === 'AbortError') return
      toast.error(t('search:replace.failed', { message: errorMessage(lt, e) }))
      setPhase({ kind: 'idle' })
    }
  }

  const runApply = async (preview: ReplacePreviewResult) => {
    const files = Object.keys(selected).filter((p) => selected[p])
    if (files.length === 0) return
    const ctl = new AbortController()
    abort.current = ctl
    setPhase({ kind: 'applying', preview })
    try {
      const all = preview.files.filter((f) => !f.skipped).length === files.length
      const { task } = await applyReplace(preview.previewId, all ? undefined : files)
      upsert(task)
      const final = await waitForTask(task.id, ctl.signal)
      upsert(final)
      if (final.state !== 'done') {
        toast.error(t('search:replace.failed', { message: final.error?.message ?? final.state }))
        setPhase({ kind: 'previewed', preview })
        return
      }
      const result = final.result as unknown as ReplaceApplyResult
      setPhase({ kind: 'done', result })
      toast.success(
        t('search:replace.done', {
          replacements: result.replacements,
          files: result.files.filter((f) => f.status === 'ok').length,
          skipped: result.skipped,
        }),
      )
      onApplied()
    } catch (e) {
      if ((e as { name?: string }).name === 'AbortError') return
      toast.error(t('search:replace.failed', { message: errorMessage(lt, e) }))
      setPhase({ kind: 'previewed', preview })
    }
  }

  const undo = async (result: ReplaceApplyResult) => {
    if (result.versionIds.length === 0) {
      toast.info(t('search:replace.noVersions'))
      return
    }
    setUndoing(true)
    let ok = 0
    let failed = 0
    for (const id of result.versionIds) {
      try {
        await restoreVersion(id)
        ok++
      } catch {
        failed++
      }
    }
    setUndoing(false)
    if (ok) toast.success(t('search:replace.undone', { n: ok }))
    if (failed) toast.error(t('search:replace.undoFailed', { n: failed }))
    setPhase({ kind: 'done', result, undone: true })
    onApplied()
  }

  const selectedCount = Object.values(selected).filter(Boolean).length
  const busy = phase.kind === 'previewing' || phase.kind === 'applying'

  return (
    <aside
      className="flex min-h-0 w-full flex-col border-t border-border bg-surface lg:w-[28rem] lg:border-t-0 lg:border-l"
      aria-label={t('search:replace.title')}
      data-testid="replace-panel"
    >
      <header className="flex items-center gap-2 border-b border-border px-3 py-2">
        <Replace className="size-4" aria-hidden="true" />
        <h2 className="text-sm font-semibold">{t('search:replace.title')}</h2>
        <Button
          size="xs"
          variant="ghost"
          className="ml-auto"
          onClick={onClose}
          data-testid="replace-close"
        >
          {t('search:replace.cancel')}
        </Button>
      </header>
      <div className="grid gap-2 border-b border-border px-3 py-2">
        <Label htmlFor="replace-with">{t('search:replace.with')}</Label>
        <div className="flex gap-2">
          <Input
            id="replace-with"
            className="h-8 font-mono"
            value={replacement}
            placeholder={t('search:replace.placeholder')}
            onChange={(e) => setReplacement(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void runPreview()
            }}
            disabled={busy}
            data-testid="replace-input"
          />
          <Button
            size="sm"
            onClick={() => void runPreview()}
            disabled={busy || !query.q}
            data-testid="replace-preview"
          >
            {phase.kind === 'previewing'
              ? t('search:replace.previewing')
              : t('search:replace.preview')}
          </Button>
        </div>
        {replacement === '' ? (
          <p className="text-xs text-text-muted">{t('search:replace.emptyReplace')}</p>
        ) : null}
      </div>
      {phase.kind === 'previewed' || phase.kind === 'applying' ? (
        <>
          <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2 text-xs text-text-muted">
            <span data-testid="replace-summary">
              {t('search:replace.summary', {
                replacements: phase.preview.totalReplacements,
                files: phase.preview.totalFiles,
              })}
            </span>
            <span>
              ·{' '}
              {t('search:replace.expires', {
                at: formatDateTimeFull(phase.preview.expiresAt, lang),
              })}
            </span>
            <label className="ml-auto flex items-center gap-1">
              <Checkbox
                checked={selectedCount === phase.preview.files.filter((f) => !f.skipped).length}
                onCheckedChange={(c) => {
                  const sel: Record<string, boolean> = {}
                  for (const f of phase.preview.files) if (!f.skipped) sel[f.path] = c === true
                  setSelected(sel)
                }}
                aria-label={t('search:replace.selectAll')}
              />
              {t('search:replace.selectAll')}
            </label>
          </div>
          <div className="min-h-0 flex-1 overflow-auto" data-testid="replace-files">
            {phase.preview.files.map((f) => (
              <section
                key={f.path}
                className="border-b border-border"
                data-testid="replace-file"
                data-path={f.path}
              >
                <label className="flex items-center gap-2 px-3 py-1.5 text-sm">
                  <Checkbox
                    checked={!!selected[f.path]}
                    disabled={!!f.skipped || busy}
                    onCheckedChange={(c) => setSelected((s) => ({ ...s, [f.path]: c === true }))}
                    data-testid="replace-file-check"
                  />
                  <span className="truncate font-mono text-xs" title={f.path}>
                    {f.path}
                  </span>
                  <span className="ml-auto rounded-full bg-surface-2 px-2 text-xs tabular text-text-muted">
                    {f.replacements}
                  </span>
                </label>
                {f.skipped ? (
                  <p className="px-3 pb-2 text-xs text-warning">
                    {t('search:replace.skipped', {
                      reason: t(`search:replace.skippedReason.${f.skipped}`),
                    })}
                  </p>
                ) : f.hunks?.length ? (
                  <DiffPreview hunks={f.hunks} hunksTotal={f.hunksTotal} />
                ) : (
                  <p className="px-3 pb-2 text-xs text-text-muted">
                    {t('search:replace.countOnly', { n: f.hunksTotal })}
                  </p>
                )}
              </section>
            ))}
          </div>
          <footer className="border-t border-border px-3 py-2">
            <Button
              className="w-full"
              onClick={() => void runApply(phase.preview)}
              disabled={busy || selectedCount === 0}
              data-testid="replace-apply"
            >
              {phase.kind === 'applying'
                ? t('search:replace.applying')
                : t('search:replace.apply', { n: selectedCount })}
            </Button>
          </footer>
        </>
      ) : null}
      {phase.kind === 'done' ? (
        <div className="grid gap-2 px-3 py-3 text-sm" data-testid="replace-result">
          <p>
            {t('search:replace.done', {
              replacements: phase.result.replacements,
              files: phase.result.files.filter((f) => f.status === 'ok').length,
              skipped: phase.result.skipped,
            })}
          </p>
          <ul className="max-h-48 overflow-auto font-mono text-xs text-text-muted">
            {phase.result.files.map((f) => (
              <li key={f.path} className="truncate">
                {f.status === 'ok' ? '✓' : '–'} {f.path}
                {f.reason
                  ? ` (${t(`search:replace.skippedReason.${f.reason}`, { defaultValue: f.reason })})`
                  : ''}
              </li>
            ))}
          </ul>
          {!phase.undone ? (
            <Button
              variant="outline"
              onClick={() => void undo(phase.result)}
              disabled={undoing}
              data-testid="replace-undo"
            >
              <Undo2 aria-hidden="true" />
              {undoing ? t('search:replace.undoing') : t('search:replace.undo')}
            </Button>
          ) : null}
        </div>
      ) : null}
    </aside>
  )
}
