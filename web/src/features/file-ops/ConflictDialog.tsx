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
import { Label } from '@/components/ui/label'
import { formatBytes, formatDateTimeFull, formatNumber } from '@/lib/format'
import type { Conflict, ConflictPolicy, ConflictStat, TransferOp } from './api'
import { baseName } from '@/features/browser/path'

export interface ConflictDialogProps {
  op: TransferOp
  conflicts: Conflict[]
  /** `perItem` keyed by source path, or null when cancelled. */
  onResolve: (perItem: Record<string, ConflictPolicy> | null) => void
  onCloseAutoFocus?: (e: Event) => void
}

function Side({ title, stat, lang }: { title: string; stat: ConflictStat; lang: string }) {
  const { t } = useTranslation('fileops')
  return (
    <div className="rounded-md border border-border bg-surface-2 p-3 text-xs">
      <div className="mb-1 font-medium text-text">{title}</div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-text-muted">
        <dt>{t('conflict.size')}</dt>
        <dd className="tabular">
          {stat.isDir ? t('conflict.folder') : formatBytes(stat.size, lang)}
        </dd>
        <dt>{t('conflict.mtime')}</dt>
        <dd className="tabular">{stat.mtime ? formatDateTimeFull(stat.mtime, lang) : '—'}</dd>
      </dl>
    </div>
  )
}

/** Conflict resolution (SPEC §8.5 policy per item): Timpa / Lewati / Ganti
 *  nama with a comparison of both sides; "Terapkan ke semua" answers the
 *  remaining conflicts in one go. */
export function ConflictDialog({
  op,
  conflicts,
  onResolve,
  onCloseAutoFocus,
}: ConflictDialogProps) {
  const { t, i18n } = useTranslation('fileops')
  const lang = i18n.resolvedLanguage ?? 'id'
  const [index, setIndex] = useState(0)
  const [applyAll, setApplyAll] = useState(false)
  const [decided, setDecided] = useState<Record<string, ConflictPolicy>>({})
  const total = conflicts.length
  const cur = conflicts[Math.min(index, total - 1)]
  if (!cur) return null
  const bothDirs = cur.existing.isDir && cur.incoming.isDir
  const mismatch = cur.existing.isDir !== cur.incoming.isDir

  const choose = (policy: ConflictPolicy) => {
    const next = { ...decided, [cur.source]: policy }
    if (applyAll) {
      for (const c of conflicts) if (!(c.source in next)) next[c.source] = policy
      onResolve(next)
      return
    }
    if (index + 1 < total) {
      setDecided(next)
      setIndex(index + 1)
      return
    }
    onResolve(next)
  }

  return (
    <Dialog open onOpenChange={(o) => (!o ? onResolve(null) : undefined)}>
      <DialogContent
        className="sm:max-w-lg"
        data-testid="conflict-dialog"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <DialogHeader>
          <DialogTitle>{t('conflict.title', { name: baseName(cur.path) })}</DialogTitle>
          <DialogDescription>
            {t(op === 'move' ? 'conflict.descriptionMove' : 'conflict.descriptionCopy', {
              dir: cur.path.slice(0, cur.path.lastIndexOf('/')) || '/',
            })}
            {total > 1 ? (
              <span className="ml-1 tabular" data-testid="conflict-counter">
                (
                {t('conflict.counter', {
                  i: formatNumber(index + 1, lang),
                  n: formatNumber(total, lang),
                })}
                )
              </span>
            ) : null}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 sm:grid-cols-2">
          <Side title={t('conflict.existing')} stat={cur.existing} lang={lang} />
          <Side title={t('conflict.incoming')} stat={cur.incoming} lang={lang} />
        </div>
        {mismatch ? (
          <p className="text-xs text-warning" role="note">
            {t('conflict.typeMismatch')}
          </p>
        ) : null}
        {total > 1 ? (
          <div className="flex items-center gap-2">
            <Checkbox
              id="conflict-apply-all"
              checked={applyAll}
              onCheckedChange={(v) => setApplyAll(v === true)}
              data-testid="conflict-apply-all"
            />
            <Label htmlFor="conflict-apply-all" className="text-sm font-normal">
              {t('conflict.applyAll', {
                count: total - index,
                n: formatNumber(total - index, lang),
              })}
            </Label>
          </div>
        ) : null}
        <DialogFooter className="flex-wrap gap-2 sm:justify-between">
          <Button variant="outline" onClick={() => onResolve(null)} data-testid="conflict-cancel">
            {t('conflict.cancel')}
          </Button>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={() => choose('skip')} data-testid="conflict-skip">
              {t('conflict.skip')}
            </Button>
            <Button
              variant="outline"
              onClick={() => choose('rename')}
              data-testid="conflict-rename"
            >
              {t('conflict.rename')}
            </Button>
            <Button
              variant={bothDirs ? 'primary' : 'danger'}
              disabled={mismatch}
              onClick={() => choose('overwrite')}
              data-testid="conflict-overwrite"
              autoFocus
            >
              {bothDirs ? t('conflict.merge') : t('conflict.overwrite')}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
