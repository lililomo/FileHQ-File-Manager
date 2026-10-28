import { useTranslation } from 'react-i18next'
import type { TransferOp } from '@/features/file-ops/api'
import { baseName, ROOT } from './path'

/** Visual-only badge (the drag itself is pointer-only; SPEC §8.5 alternatives
 *  "Pindah/Salin ke…" and "… ke panel lain" cover keyboard users). */
export function PaneDropBadge({ op, dir }: { op: TransferOp; dir: string }) {
  const { t } = useTranslation('files')
  const name = dir === ROOT ? t('nav.root') : baseName(dir)
  return (
    <div
      className="pointer-events-none absolute inset-0 z-20 flex items-end justify-center rounded-md border-2 border-dashed border-accent bg-accent/5 p-4"
      data-testid="pane-drop"
      data-op={op}
      aria-hidden="true"
    >
      <span className="rounded-md bg-accent px-3 py-1 text-sm font-medium text-accent-fg shadow">
        {t(op === 'copy' ? 'dualPane.dropCopy' : 'dualPane.dropMove', { name })}
      </span>
    </div>
  )
}
