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
import type { Item } from './api'
import { ItemFacts, ItemHeader } from './ItemDetails'
import { useItemStat } from './useItemStat'

/** Quick Look (Space): a lightweight item card until the real viewers land in Step 17. */
export function QuickLookDialog({ item, onClose }: { item: Item | null; onClose: () => void }) {
  const { t } = useTranslation('files')
  return (
    <Dialog open={!!item} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md" data-testid="quick-look">
        <DialogHeader>
          <DialogTitle>{t('quickLook.title')}</DialogTitle>
          <DialogDescription>{t('quickLook.description')}</DialogDescription>
        </DialogHeader>
        {item ? (
          <div className="space-y-4">
            <ItemHeader item={item} size={64} />
            <ItemFacts item={item} />
          </div>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('quickLook.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** Properties (Alt+Enter): full stat; notes/tags/versions/checksum arrive in 10a/10b. */
export function PropertiesDialog({ item, onClose }: { item: Item | null; onClose: () => void }) {
  const { t } = useTranslation('files')
  const stat = useItemStat(item?.path ?? null)
  const warnings = stat.data?.warnings ?? []
  return (
    <Dialog open={!!item} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg" data-testid="properties">
        <DialogHeader>
          <DialogTitle>{t('props.title')}</DialogTitle>
          <DialogDescription>{item?.path}</DialogDescription>
        </DialogHeader>
        {item ? (
          <div className="space-y-4">
            <ItemHeader item={item} size={48} />
            <ItemFacts
              item={item}
              stat={stat.data?.stat}
              versionsCount={stat.data?.versionsCount}
            />
            {warnings.length > 0 ? (
              <p className="text-xs text-warning" role="note">
                {t('props.warning', { codes: warnings.join(', ') })}
              </p>
            ) : null}
          </div>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('quickLook.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
