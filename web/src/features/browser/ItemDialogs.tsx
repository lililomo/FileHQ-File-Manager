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
