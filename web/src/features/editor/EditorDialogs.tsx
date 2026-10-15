import { useEffect, useRef, useState } from 'react'
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
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { formatDateTimeFull } from '@/lib/format'
import { useUIStore } from '@/stores/ui'
import type { EditorTab } from '@/stores/editor'
import { ENCODINGS, getContent } from './api'
import { useWorkspace, type CloseRequest, type DiffSpec } from './controller'
import { getModel } from './models'
import { monacoTheme, type MonacoApi } from './monaco'
import { useEditorPrefs } from './prefs'

export type { DiffSpec } from './controller'

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

/** Mounts a Monaco diff editor into a div; returns the editor for reads. */
function useDiffEditor(
  host: HTMLDivElement | null,
  monaco: MonacoApi,
  spec: { path: string; original: string; modified: string } | null,
  opts: { inline: boolean; editable: boolean },
) {
  const prefs = useEditorPrefs()
  const resolved = useUIStore((s) => s.resolvedTheme)
  const ref = useRef<Monaco.editor.IStandaloneDiffEditor | null>(null)
  const modifiedRef = useRef<Monaco.editor.ITextModel | null>(null)
  useEffect(() => {
    if (!spec || !host) return
    const diff = monaco.editor.createDiffEditor(host, {
      automaticLayout: true,
      readOnly: !opts.editable,
      originalEditable: false,
      renderSideBySide: !opts.inline,
      fontFamily: `"${prefs.font_family}", monospace`,
      fontSize: prefs.font_size,
      theme: monacoTheme(prefs.theme, resolved),
      ariaLabel: 'diff',
    })
    const lang = getModel(spec.path)?.getLanguageId()
    const original = monaco.editor.createModel(
      spec.original,
      lang,
      monaco.Uri.from({ scheme: 'fm-diff', path: `/orig${spec.path}` }),
    )
    const modified = monaco.editor.createModel(
      spec.modified,
      lang,
      monaco.Uri.from({ scheme: 'fm-diff', path: `/mod${spec.path}` }),
    )
    diff.setModel({ original, modified })
    ref.current = diff
    modifiedRef.current = modified
    return () => {
      diff.dispose()
      original.dispose()
      modified.dispose()
      ref.current = null
      modifiedRef.current = null
    }
    // the models are recreated only when the spec changes; view options update below
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [spec, monaco, host])
  useEffect(() => {
    ref.current?.updateOptions({
      renderSideBySide: !opts.inline,
      readOnly: !opts.editable,
      fontFamily: `"${prefs.font_family}", monospace`,
      fontSize: prefs.font_size,
    })
  }, [opts.inline, opts.editable, prefs.font_family, prefs.font_size])
  useEffect(() => {
    monaco.editor.setTheme(monacoTheme(prefs.theme, resolved))
  }, [monaco, prefs.theme, resolved])
  return modifiedRef
}

/** 409 on save (SPEC §8.13 "Menyimpan" 1; Step 16b): the dialog embeds a
 *  Monaco diff (left: server, right: yours) with Overwrite / Reload / Merge
 *  manually (the right side becomes editable; Apply saves with force). */
export function ConflictDialog({ tab, monaco }: { tab: EditorTab | null; monaco: MonacoApi }) {
  const { t, i18n } = useTranslation('editor')
  const ws = useWorkspace()
  const open = !!tab?.conflict
  const path = tab?.path ?? ''
  const [server, setServer] = useState<{ path: string; content: string } | null>(null)
  const [merge, setMerge] = useState(false)
  const [inline, setInline] = useState(false)
  const [host, setHost] = useState<HTMLDivElement | null>(null)
  // reset per open (derived state, no setState inside an effect)
  const [prevOpen, setPrevOpen] = useState(open)
  if (open !== prevOpen) {
    setPrevOpen(open)
    setServer(null)
    setMerge(false)
  }
  useEffect(() => {
    if (!open) return
    let alive = true
    getContent(path)
      .then((c) => alive && setServer({ path, content: c.content }))
      .catch(() => alive && setServer({ path, content: '' }))
    return () => {
      alive = false
    }
  }, [open, path])
  const spec =
    server && server.path === path
      ? { path, original: server.content, modified: getModel(path)?.getValue() ?? '' }
      : null
  const modifiedRef = useDiffEditor(host, monaco, spec, { inline, editable: merge })
  const close = () => tab && ws.clearConflict(tab.path)
  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent
        className="flex h-[85vh] max-w-[95vw] flex-col sm:max-w-[95vw]"
        data-testid="conflict-dialog"
      >
        <DialogHeader>
          <DialogTitle>{t('dialog.conflictTitle')}</DialogTitle>
          <DialogDescription>
            {t('dialog.conflictDesc', {
              mtime: tab?.conflict?.serverMtime
                ? formatDateTimeFull(tab.conflict.serverMtime, i18n.resolvedLanguage ?? 'id')
                : '?',
            })}
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-4 text-xs text-text-muted">
          <span className="flex-1">{t('dialog.diffLeft')}</span>
          <span className="flex-1">
            {merge ? t('dialog.diffMergeRight') : t('dialog.diffRight')}
          </span>
          <label className="flex items-center gap-2">
            <Switch
              checked={inline}
              onCheckedChange={setInline}
              aria-label={t('dialog.diffInline')}
            />
            {t('dialog.diffInline')}
          </label>
        </div>
        <div ref={setHost} className="min-h-0 flex-1 rounded-md border border-border">
          {!spec ? (
            <p className="p-4 text-sm text-text-muted">{t('dialog.conflictLoading')}</p>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={close}>
            {t('dialog.cancel')}
          </Button>
          <Button
            variant="outline"
            onClick={() => tab && void ws.resolveConflict(tab.path, 'reload')}
            data-testid="conflict-reload"
          >
            {t('dialog.reload')}
          </Button>
          {merge ? (
            <Button
              onClick={() => {
                const merged = modifiedRef.current?.getValue()
                if (merged === undefined || !tab) return
                const model = getModel(tab.path)
                model?.setValue(merged)
                ws.clearConflict(tab.path)
                void ws.save(tab.path, { force: true })
              }}
              data-testid="conflict-apply"
            >
              {t('dialog.conflictApply')}
            </Button>
          ) : (
            <Button variant="outline" onClick={() => setMerge(true)} data-testid="conflict-merge">
              {t('dialog.conflictMerge')}
            </Button>
          )}
          <Button
            onClick={() => tab && void ws.resolveConflict(tab.path, 'overwrite')}
            data-testid="conflict-overwrite"
          >
            {t('dialog.overwrite')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** Monaco diff editor in a dialog: draft vs current, a stored version vs
 *  current, or an editable merge (Apply hands the right side back). */
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
  const [inline, setInline] = useState(false)
  const [host, setHost] = useState<HTMLDivElement | null>(null)
  const editable = spec?.kind === 'merge'
  const modifiedRef = useDiffEditor(host, monaco, spec, { inline, editable: !!editable })
  const left =
    spec?.leftLabel ?? (spec?.kind === 'draft' ? t('dialog.diffDraftLeft') : t('dialog.diffLeft'))
  const right =
    spec?.rightLabel ??
    (spec?.kind === 'draft'
      ? t('dialog.diffDraftRight')
      : spec?.kind === 'version'
        ? t('dialog.diffVersionRight')
        : spec?.kind === 'merge'
          ? t('dialog.diffMergeRight')
          : t('dialog.diffRight'))
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
        <div className="flex items-center gap-4 text-xs text-text-muted">
          <span className="flex-1" data-testid="diff-left-label">
            {left}
          </span>
          <span className="flex-1">{right}</span>
          <label className="flex items-center gap-2">
            <Switch
              checked={inline}
              onCheckedChange={setInline}
              aria-label={t('dialog.diffInline')}
              data-testid="diff-inline"
            />
            {t('dialog.diffInline')}
          </label>
        </div>
        <div ref={setHost} className="min-h-0 flex-1 rounded-md border border-border" />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('dialog.close')}
          </Button>
          {spec?.onApply ? (
            <Button
              onClick={() => {
                const v = modifiedRef.current?.getValue()
                if (v === undefined) return
                void spec.onApply?.(v)
                onClose()
              }}
              data-testid="diff-apply"
            >
              {t('dialog.conflictApply')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** "Reopen with encoding" picker used by the Monaco action / F1 palette. */
export function EncodingPickerDialog({
  path,
  onClose,
}: {
  path: string | null
  onClose: () => void
}) {
  const { t } = useTranslation('editor')
  const ws = useWorkspace()
  const current = path ? (ws.tab(path)?.encoding ?? 'utf-8') : 'utf-8'
  const [enc, setEnc] = useState(current)
  const [prevPath, setPrevPath] = useState(path)
  if (path !== prevPath) {
    setPrevPath(path)
    setEnc(current)
  }
  return (
    <Dialog open={!!path} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-sm" data-testid="encoding-picker">
        <DialogHeader>
          <DialogTitle>{t('dialog.encodingTitle')}</DialogTitle>
        </DialogHeader>
        <Label htmlFor="enc-pick">{t('dialog.encodingPick')}</Label>
        <Select value={enc} onValueChange={setEnc}>
          <SelectTrigger id="enc-pick">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {ENCODINGS.map((e) => (
              <SelectItem key={e} value={e}>
                {e}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('dialog.cancel')}
          </Button>
          <Button
            onClick={() => {
              if (path) void ws.reopenWithEncoding(path, enc)
              onClose()
            }}
          >
            {t('dialog.apply')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
