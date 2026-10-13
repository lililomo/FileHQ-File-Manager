import { cn } from '@/lib/utils'
import { useUIStore } from '@/stores/ui'
import { QuotaIndicator } from './QuotaIndicator'
import { SidebarNav } from './SidebarNav'

/** 232 px sidebar, 48 px when collapsed (SPEC §9.1 sizes). Hidden below md (drawer instead). */
export function Sidebar() {
  const collapsed = useUIStore((s) => s.sidebarCollapsed)
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
