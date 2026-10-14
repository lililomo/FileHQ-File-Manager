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
import type { UploadPrecheckInfo } from '@/features/file-ops/store'
import { formatBytes, formatNumber } from '@/lib/format'
import type { PrecheckItem } from './api'

type LooseT = (key: string, options?: Record<string, unknown>) => string

function reasonText(t: LooseT, it: PrecheckItem, lang: string, maxUploadSize: number): string {
  switch (it.status) {
    case 'blocked_ext':
      return t('upload:precheck.reason.blocked', { ext: it.reason ?? '' })
    case 'too_large':
      return t('upload:precheck.reason.tooLarge', { max: formatBytes(maxUploadSize, lang) })
    case 'name_invalid':
      return t(`errors:reason.${it.reason ?? 'invalid'}`, {
        defaultValue: t('upload:precheck.reason.invalid'),
      })
    default:
      return it.reason ?? ''
  }
}

/** Shown before an upload starts when some files were rejected by the
 *  precheck or the quota is short (SPEC §8.8 step 2). */
export function PrecheckDialog({
  info,
  onResolve,
  onCloseAutoFocus,
}: {
  info: UploadPrecheckInfo
  onResolve: (proceed: boolean) => void
  onCloseAutoFocus?: (e: Event) => void
}) {
  const { t, i18n } = useTranslation(['upload', 'errors', 'common'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const quotaShort = !!info.quotaReason
  const canContinue = info.fits > 0
  const shown = info.rejected.slice(0, 50)
  return (
    <Dialog open onOpenChange={(o) => (!o ? onResolve(false) : undefined)}>
      <DialogContent data-testid="upload-precheck" onCloseAutoFocus={onCloseAutoFocus}>
        <DialogHeader>
          <DialogTitle>
            {quotaShort ? lt('upload:precheck.quotaTitle') : lt('upload:precheck.title')}
          </DialogTitle>
          <DialogDescription>
            {quotaShort
              ? lt(
                  info.quotaReason === 'disk'
                    ? 'upload:precheck.diskShort'
                    : 'upload:precheck.quotaShort',
                  {
                    needed: formatBytes(info.needed, lang),
                    free: formatBytes(info.free, lang),
                  },
                )
              : lt('upload:precheck.rejectedSummary', {
                  count: info.rejected.length,
                  n: formatNumber(info.rejected.length, lang),
                  accepted: formatNumber(info.accepted, lang),
                })}
          </DialogDescription>
        </DialogHeader>
        {info.rejected.length > 0 ? (
          <ul
            className="max-h-56 overflow-auto rounded-md border border-border bg-surface-2 p-2 text-xs"
            data-testid="upload-precheck-rejected"
          >
            {shown.map((it) => (
              <li
                key={`${it.relativePath ?? ''}/${it.name}`}
                className="flex justify-between gap-3 py-0.5"
              >
                <span className="truncate text-text">
                  {it.relativePath ? `${it.relativePath}/` : ''}
                  {it.name}
                </span>
                <span className="shrink-0 text-text-muted">
                  {reasonText(lt, it, lang, info.maxUploadSize)}
                </span>
              </li>
            ))}
            {info.rejected.length > shown.length ? (
              <li className="pt-1 text-text-muted">
                {lt('upload:precheck.more', {
                  n: formatNumber(info.rejected.length - shown.length, lang),
                })}
              </li>
            ) : null}
          </ul>
        ) : null}
        {quotaShort && canContinue ? (
          <p className="text-sm text-text-muted">
            {lt('upload:precheck.partialHint', { n: formatNumber(info.fits, lang) })}
          </p>
        ) : null}
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onResolve(false)}
            data-testid="upload-precheck-cancel"
          >
            {lt('common:actions.cancel')}
          </Button>
          {canContinue ? (
            <Button
              onClick={() => onResolve(true)}
              data-testid="upload-precheck-continue"
              autoFocus
            >
              {quotaShort
                ? lt('upload:precheck.continueFits', { n: formatNumber(info.fits, lang) })
                : lt('upload:precheck.continue', { n: formatNumber(info.fits, lang) })}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
