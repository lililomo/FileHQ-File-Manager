import { useIsTablet } from '@/lib/useMediaQuery'
import { cn } from '@/lib/utils'
import { useUIStore } from '@/stores/ui'
import { QuotaIndicator } from './QuotaIndicator'
import { SidebarNav } from './SidebarNav'

/** 232 px sidebar, 48 px when collapsed (SPEC §9.1 sizes). Hidden below md
 *  (drawer instead); 768–1279 px it is 48 px icons until the top-bar toggle
 *  expands it (SPEC §9.9). */
export function Sidebar() {
  const tablet = useIsTablet()
  const collapsedPref = useUIStore((s) => s.sidebarCollapsed)
  const tabletExpanded = useUIStore((s) => s.sidebarTabletExpanded)
  const collapsed = tablet ? !tabletExpanded : collapsedPref
  return (
    <aside
      className={cn(
        'hidden shrink-0 flex-col border-r border-border bg-surface transition-[width] md:flex',
        collapsed ? 'w-12' : 'w-58',
      )}
      data-collapsed={collapsed || undefined}
    >
      <SidebarNav collapsed={collapsed} />
      <div className={cn('mt-auto border-t border-border', collapsed ? 'p-2' : 'p-3')}>
        <QuotaIndicator collapsed={collapsed} />
      </div>
    </aside>
  )
}
