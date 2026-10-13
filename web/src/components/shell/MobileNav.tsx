import { Menu } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import { SidebarNav } from './SidebarNav'
import { QuotaIndicator } from './QuotaIndicator'

/** Drawer navigation below the md breakpoint (SPEC §9.9). */
export function MobileNav() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="text-text-muted md:hidden"
          aria-label={t('actions.openMenu')}
        >
          <Menu aria-hidden="true" />
        </Button>
      </SheetTrigger>
      <SheetContent side="left" className="w-64 p-0">
        <SheetHeader className="sr-only">
          <SheetTitle>{t('a11y.mainNavigation')}</SheetTitle>
          <SheetDescription>{t('app.tagline')}</SheetDescription>
        </SheetHeader>
        <div className="flex h-full flex-col pt-8">
          <SidebarNav onNavigate={() => setOpen(false)} />
          <div className="mt-auto border-t border-border p-3">
            <QuotaIndicator />
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
