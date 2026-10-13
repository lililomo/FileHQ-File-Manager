import * as React from 'react'
import { Button } from '@/components/ui/button'
import { Kbd } from '@/components/ui/kbd'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

export interface IconButtonProps extends React.ComponentProps<typeof Button> {
  /** Accessible name; also the tooltip text (SPEC §9.2: tooltip = label + shortcut). */
  label: string
  shortcut?: string
  tooltipSide?: 'top' | 'bottom' | 'left' | 'right'
}

/** 32 px icon-only button with mandatory label/tooltip. */
export const IconButton = React.forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  {
    label,
    shortcut,
    tooltipSide = 'bottom',
    className,
    variant = 'ghost',
    size = 'icon',
    children,
    ...props
  },
  ref,
) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          ref={ref}
          aria-label={label}
          variant={variant}
          size={size}
          className={cn('text-text-muted hover:text-text', className)}
          {...props}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent side={tooltipSide}>
        <span>{label}</span>
        {shortcut ? <Kbd className="ml-2">{shortcut}</Kbd> : null}
      </TooltipContent>
    </Tooltip>
  )
})
