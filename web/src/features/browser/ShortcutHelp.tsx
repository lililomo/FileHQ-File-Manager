import { useTranslation } from 'react-i18next'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Kbd, KbdGroup } from '@/components/ui/kbd'
import { SHORTCUTS, formatKeys, type ShortcutScope } from '@/lib/shortcuts'

const SCOPES: ShortcutScope[] = ['browser', 'global', 'editor', 'viewer', 'video']

/** "?" dialog listing every registered shortcut (SPEC §9.6). */
export function ShortcutHelp({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const { t } = useTranslation('files')
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-h-[85vh] overflow-auto sm:max-w-2xl"
        data-testid="shortcut-help"
      >
        <DialogHeader>
          <DialogTitle>{t('shortcuts.title')}</DialogTitle>
          <DialogDescription>{t('shortcuts.description')}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-6 sm:grid-cols-2">
          {SCOPES.map((scope) => {
            const defs = SHORTCUTS.filter((s) => s.scope === scope)
            if (defs.length === 0) return null
            return (
              <section key={scope} aria-labelledby={`sc-${scope}`}>
                <h3
                  id={`sc-${scope}`}
                  className="mb-2 text-xs font-semibold text-text-muted uppercase"
                >
                  {t(`shortcuts.scope.${scope}` as 'shortcuts.scope.browser')}
                </h3>
                <dl className="space-y-1 text-sm">
                  {defs.map((s) => (
                    <div
                      key={s.id}
                      className="flex items-center justify-between gap-3"
                      data-shortcut={s.id}
                    >
                      <dt className={s.planned ? 'text-text-muted' : ''}>
                        {t(`shortcuts.${s.labelKey}` as 'shortcuts.copy')}
                        {s.planned ? (
                          <span className="ml-1 text-xs text-text-muted">
                            {t('shortcuts.planned')}
                          </span>
                        ) : null}
                      </dt>
                      <dd className="flex shrink-0 items-center gap-1">
                        {[s.keys, ...(s.alt?.slice(0, 1) ?? [])].map((k, i) => (
                          <KbdGroup key={k}>
                            {i > 0 ? <span className="text-xs text-text-muted">/</span> : null}
                            {formatKeys(k).map((part, j) => (
                              <Kbd key={`${k}-${j}`}>{part}</Kbd>
                            ))}
                          </KbdGroup>
                        ))}
                      </dd>
                    </div>
                  ))}
                </dl>
              </section>
            )
          })}
        </div>
      </DialogContent>
    </Dialog>
  )
}
