import { FilePlus2, X } from 'lucide-react'
import { useState } from 'react'
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
import { FolderPicker } from '@/features/file-ops/FolderPicker'

export interface AddFilesDialogProps {
  archivePath: string
  archiveName: string
  /** folder inside the archive the items land in ("" = root) */
  innerDir: string
  error?: string | null | undefined
  busy?: boolean | undefined
  onSubmit: (sources: string[]) => void
  onClose: () => void
}

/** "Add files…" of the archive browser (SPEC §8.11, zip only): pick files
 *  or folders from the user's root one by one (Enter / double-click adds to
 *  the list), then queue the zip rewrite. */
export function AddFilesDialog({
  archivePath,
  archiveName,
  innerDir,
  error,
  busy,
  onSubmit,
  onClose,
}: AddFilesDialogProps) {
  const { t } = useTranslation(['archive', 'fileops'])
  const [current, setCurrent] = useState<string | null>(null)
  const [chosen, setChosen] = useState<string[]>([])
  const add = (p: string | null) => {
    if (!p || p === '/' || p === archivePath || chosen.includes(p)) return
    setChosen((c) => [...c, p])
  }
  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent className="sm:max-w-lg" data-testid="archive-add-dialog">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FilePlus2 aria-hidden="true" className="size-4" />
            {t('archive:browser.add.title')}
          </DialogTitle>
          <DialogDescription className="truncate">
            {t('archive:browser.add.description', { name: archiveName, dir: innerDir || '/' })}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (chosen.length) onSubmit(chosen)
          }}
        >
          <FolderPicker
            value={current}
            onChange={setCurrent}
            onActivate={add}
            mode="all"
            isDisabled={(p) => p === archivePath}
            className="max-h-48"
            autoFocus
          />
          <div className="flex items-center gap-2">
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={!current || current === '/'}
              onClick={() => add(current)}
              data-testid="archive-add-pick"
            >
              {t('archive:browser.add.pick')}
            </Button>
            <span className="text-xs text-text-muted">
              {t('archive:browser.add.count', { count: chosen.length })}
            </span>
          </div>
          {chosen.length ? (
            <ul className="max-h-32 space-y-1 overflow-auto text-xs" data-testid="archive-add-list">
              {chosen.map((p) => (
                <li key={p} className="flex items-center gap-2 rounded bg-surface-2 px-2 py-1">
                  <span className="min-w-0 flex-1 truncate font-mono" title={p}>
                    {p}
                  </span>
                  <button
                    type="button"
                    className="rounded p-0.5 text-text-muted hover:text-text"
                    onClick={() => setChosen((c) => c.filter((x) => x !== p))}
                    aria-label={t('archive:browser.add.remove', { path: p })}
                  >
                    <X className="size-3" aria-hidden="true" />
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
          {error ? (
            <p className="text-xs text-danger" role="alert" data-testid="archive-add-error">
              {error}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('fileops:cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={!chosen.length || !!busy}
              data-testid="archive-add-submit"
            >
              {t('archive:browser.add.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
