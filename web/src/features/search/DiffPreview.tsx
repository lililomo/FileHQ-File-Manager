import { useTranslation } from 'react-i18next'
import type { ReplaceHunk } from './api'

/** Lightweight before/after view of changed lines (no Monaco): one red and
 *  one green row per hunk, line numbers on the left. */
export function DiffPreview({ hunks, hunksTotal }: { hunks: ReplaceHunk[]; hunksTotal: number }) {
  const { t } = useTranslation(['search'])
  return (
    <div className="overflow-x-auto font-mono text-xs" data-testid="diff-preview">
      {hunks.map((h) => (
        <div key={h.line} className="grid grid-cols-[3rem_1fr]">
          <span className="row-span-2 border-r border-border pr-2 text-right tabular text-text-muted">
            {h.line}
          </span>
          <div
            className="bg-destructive/10 px-2 whitespace-pre text-destructive"
            data-testid="diff-before"
          >
            <span className="select-none" aria-hidden="true">
              -{' '}
            </span>
            {h.before}
          </div>
          <div className="bg-success/10 px-2 whitespace-pre text-success" data-testid="diff-after">
            <span className="select-none" aria-hidden="true">
              +{' '}
            </span>
            {h.after}
          </div>
        </div>
      ))}
      {hunksTotal > hunks.length ? (
        <div className="px-2 py-1 text-text-muted">
          {t('search:replace.moreHunks', { n: hunksTotal - hunks.length })}
        </div>
      ) : null}
    </div>
  )
}
