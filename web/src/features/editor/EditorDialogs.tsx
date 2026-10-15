import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type * as Monaco from 'monaco-editor'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { formatDateTimeFull } from '@/lib/format'
import { useUIStore } from '@/stores/ui'
import type { EditorTab } from '@/stores/editor'
import { useWorkspace, type CloseRequest } from './controller'
import { monacoTheme, type MonacoApi } from './monaco'
import { useEditorPrefs } from './prefs'

/** Close with unsaved changes: Save / Don't save / Cancel (SPEC §8.13). */
export function CloseDirtyDialog({ req }: { req: CloseRequest | null }) {
  const { t } = useTranslation('editor')
  const ws = useWorkspace()
  const dirty = req ? req.paths.filter((p) => ws.tab(p)?.dirty) : []
  const name = dirty[0]?.split('/').pop() ?? ''
  return (
    <AlertDialog
      open={!!req}
      onOpenChange={(o) => !o && req && void ws.resolveClose(req, 'cancel')}
    >
      <AlertDialogContent data-testid="close-dirty">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {dirty.length > 1
              ? t('dialog.closeDirtyManyTitle', { n: dirty.length })
              : t('dialog.closeDirtyTitle', { name })}
          </AlertDialogTitle>
          <AlertDialogDescription>{t('dialog.closeDirtyDesc')}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => req && void ws.resolveClose(req, 'cancel')}>
            {t('dialog.cancel')}
          </AlertDialogCancel>
          <Button
            variant="outline"
            onClick={() => req && void ws.resolveClose(req, 'discard')}
            data-testid="close-discard"
          >
            {t('dialog.dontSave')}
          </Button>
          <AlertDialogAction
            onClick={() => req && void ws.resolveClose(req, 'save')}
            data-testid="close-save"
          >
            {t('dialog.save')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** 409 on save: Overwrite / Reload / View diff. */
export function ConflictDialog({ tab }: { tab: EditorTab | null }) {
  const { t, i18n } = useTranslation('editor')
  const ws = useWorkspace()
  const open = !!tab?.conflict
  return (
    <AlertDialog open={open} onOpenChange={(o) => !o && tab && ws.clearConflict(tab.path)}>
      <AlertDialogContent data-testid="conflict-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle>{t('dialog.conflictTitle')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('dialog.conflictDesc', {
              mtime: tab?.conflict?.serverMtime
                ? formatDateTimeFull(tab.conflict.serverMtime, i18n.resolvedLanguage ?? 'id')
                : '?',
            })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => tab && ws.clearConflict(tab.path)}>
            {t('dialog.cancel')}
          </AlertDialogCancel>
          <Button
            variant="outline"
            onClick={() => tab && void ws.resolveConflict(tab.path, 'diff')}
          >
            {t('dialog.viewDiff')}
          </Button>
          <Button
            variant="outline"
            onClick={() => tab && void ws.resolveConflict(tab.path, 'reload')}
            data-testid="conflict-reload"
          >
            {t('dialog.reload')}
          </Button>
          <AlertDialogAction
            onClick={() => tab && void ws.resolveConflict(tab.path, 'overwrite')}
            data-testid="conflict-overwrite"
          >
            {t('dialog.overwrite')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

export interface DiffSpec {
  path: string
  original: string
  modified: string
  kind: 'conflict' | 'draft'
}

/** Monaco diff editor in a dialog (server vs yours / server vs draft). */
export function DiffDialog({
  spec,
  monaco,
  onClose,
}: {
  spec: DiffSpec | null
  monaco: MonacoApi
  onClose: () => void
}) {
  const { t } = useTranslation('editor')
  const prefs = useEditorPrefs()
  const resolved = useUIStore((s) => s.resolvedTheme)
  const host = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!spec || !host.current) return
    const diff: Monaco.editor.IStandaloneDiffEditor = monaco.editor.createDiffEditor(host.current, {
      automaticLayout: true,
      readOnly: true,
      originalEditable: false,
      renderSideBySide: true,
      fontFamily: `"${prefs.font_family}", monospace`,
      fontSize: prefs.font_size,
      theme: monacoTheme(prefs.theme, resolved),
    })
    const original = monaco.editor.createModel(
      spec.original,
      undefined,
      monaco.Uri.from({ scheme: 'fm-diff', path: `/orig${spec.path}` }),
    )
    const modified = monaco.editor.createModel(
      spec.modified,
      undefined,
      monaco.Uri.from({ scheme: 'fm-diff', path: `/mod${spec.path}` }),
    )
    diff.setModel({ original, modified })
    return () => {
      diff.dispose()
      original.dispose()
      modified.dispose()
    }
  }, [spec, monaco, prefs.font_family, prefs.font_size, prefs.theme, resolved])
  return (
    <Dialog open={!!spec} onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        className="flex h-[85vh] max-w-[95vw] flex-col sm:max-w-[95vw]"
        data-testid="diff-dialog"
      >
        <DialogHeader>
          <DialogTitle>
            {t('dialog.diffTitle', { name: spec?.path.split('/').pop() ?? '' })}
          </DialogTitle>
        </DialogHeader>
        <div className="flex gap-4 text-xs text-text-muted">
          <span className="flex-1">
            {spec?.kind === 'draft' ? t('dialog.diffDraftLeft') : t('dialog.diffLeft')}
          </span>
          <span className="flex-1">
            {spec?.kind === 'draft' ? t('dialog.diffDraftRight') : t('dialog.diffRight')}
          </span>
        </div>
        <div ref={host} className="min-h-0 flex-1 rounded-md border border-border" />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('dialog.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
