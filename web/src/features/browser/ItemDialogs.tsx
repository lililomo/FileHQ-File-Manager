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
import { MetaControls } from '@/features/meta/MetaControls'
import type { TagColor } from '@/features/meta/api'
import type { Item } from './api'
import { ItemFacts, ItemHeader } from './ItemDetails'
import { NoteCell } from './NoteCell'

/** Quick Look (Space): a lightweight item card until the real viewers land in Step 17. */
export function QuickLookDialog({
  item,
  onClose,
  onNoteSave,
  onToggleFavorite,
  onTogglePin,
  onSetTag,
}: {
  item: Item | null
  onClose: () => void
  onNoteSave?: ((path: string, note: string) => Promise<boolean>) | undefined
  onToggleFavorite?: ((item: Item) => void) | undefined
  onTogglePin?: ((item: Item) => void) | undefined
  onSetTag?: ((item: Item, color: TagColor | null) => void) | undefined
}) {
  const { t } = useTranslation(['files', 'meta'])
  return (
    <Dialog open={!!item} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md" data-testid="quick-look">
        <DialogHeader>
          <DialogTitle>{t('files:quickLook.title')}</DialogTitle>
          <DialogDescription>{t('files:quickLook.description')}</DialogDescription>
        </DialogHeader>
        {item ? (
          <div className="space-y-4">
            <ItemHeader item={item} size={64} />
            <MetaControls
              item={item}
              onToggleFavorite={onToggleFavorite}
              onTogglePin={onTogglePin}
              onSetTag={onSetTag}
            />
            <ItemFacts item={item} />
            {onNoteSave ? (
              <div className="flex items-center gap-2 text-xs">
                <span className="text-text-muted">{t('meta:note.title')}</span>
                <NoteCell item={item} onSave={onNoteSave} />
              </div>
            ) : null}
          </div>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('files:quickLook.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
