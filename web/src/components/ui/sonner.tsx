import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from 'lucide-react'
import { Toaster as Sonner, type ToasterProps } from 'sonner'
import { useUIStore } from '@/stores/ui'

/** Toasts sit bottom-left so they never cover the transfer panel (SPEC §9.2).
 *  Theme follows the app theme store, not next-themes. */
const Toaster = ({ ...props }: ToasterProps) => {
  const theme = useUIStore((s) => s.resolvedTheme)
  return (
    <Sonner
      theme={theme}
      position="bottom-left"
      visibleToasts={3}
      closeButton
      className="toaster group"
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      toastOptions={{ duration: 3000 }}
      style={
        {
          '--normal-bg': 'var(--bg)',
          '--normal-text': 'var(--text)',
          '--normal-border': 'var(--border)',
          '--border-radius': 'var(--radius-lg)',
          '--success-text': 'var(--success)',
          '--error-text': 'var(--danger)',
          '--warning-text': 'var(--warning)',
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
