import { Undo2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatKeys } from '@/lib/shortcuts'
import { Button } from '@/components/ui/button'
import { Kbd } from '@/components/ui/kbd'
import { UNDO_WINDOW_MS } from './store'

/** Undo toast (SPEC §9.7: 10 s with a progress ring, "Urungkan" button,
 *  Ctrl+Z while visible). The token itself lives in `useUndoStore`. */
export function UndoToastBody({
  id,
  message,
  onUndo,
}: {
  id: string | number
  message: string
  onUndo: () => void
}) {
  const { t } = useTranslation('fileops')
  return (
    <div
      className="flex w-[356px] max-w-[calc(100vw-32px)] items-center gap-3 rounded-lg border border-border bg-bg px-3 py-2 text-sm text-text shadow-lg"
      data-testid="undo-toast"
      role="status"
    >
      <svg
        className="size-5 shrink-0 -rotate-90 text-accent"
        viewBox="0 0 24 24"
        aria-hidden="true"
      >
        <circle
          cx="12"
          cy="12"
          r="10"
          fill="none"
          stroke="currentColor"
          strokeOpacity="0.2"
          strokeWidth="3"
        />
        <circle
          cx="12"
          cy="12"
          r="10"
          fill="none"
          stroke="currentColor"
          strokeWidth="3"
          strokeLinecap="round"
          className="undo-ring"
          style={{ animationDuration: `${UNDO_WINDOW_MS}ms` }}
        />
      </svg>
      <span className="min-w-0 flex-1 truncate">{message}</span>
      <Button
        size="sm"
        variant="outline"
        className="h-7 gap-1"
        onClick={() => {
          toast.dismiss(id)
          onUndo()
        }}
        data-testid="undo-button"
      >
        <Undo2 aria-hidden="true" />
        {t('undo.button')}
        <Kbd className="ml-1 hidden sm:inline-flex">{formatKeys('Ctrl+Z').join('+')}</Kbd>
      </Button>
    </div>
  )
}
