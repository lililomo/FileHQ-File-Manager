import type { LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export interface EmptyStateProps {
  icon?: LucideIcon
  title: string
  description?: string
  action?: ReactNode
  className?: string
}

/** Small icon, one sentence, optional action (SPEC §9.7). */
export function EmptyState({ icon: Icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div
      role="status"
      className={cn(
        'flex flex-col items-center justify-center gap-2 px-4 py-10 text-center text-sm',
        className,
      )}
    >
      {Icon ? (
        <span className="grid size-10 place-items-center rounded-full bg-surface-2 text-text-muted">
          <Icon className="size-5" aria-hidden="true" />
        </span>
      ) : null}
      <p className="font-medium text-text">{title}</p>
      {description ? <p className="max-w-sm text-text-muted">{description}</p> : null}
      {action ? <div className="mt-2 flex gap-2">{action}</div> : null}
    </div>
  )
}
