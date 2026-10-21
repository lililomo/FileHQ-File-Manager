/** Workspace controller (Step 16a): everything the toolbar, tabs, status bar
 *  and shortcuts do to tabs — load, save, close, encoding/EOL/language
 *  changes, drafts and conflicts. One instance per workspace, shared through
 *  WorkspaceContext. Content lives in Monaco models (models.ts). */
import { createContext, useContext, type MutableRefObject } from 'react'
import type * as Monaco from 'monaco-editor'
import { toast } from 'sonner'
import i18n from '@/i18n'
import { ApiError } from '@/lib/api'
import { errorMessage } from '@/lib/errors'
import { signUrl, startDownload } from '@/lib/media-url'
import { MAX_EDITOR_TABS, useEditorStore, type EditorTab } from '@/stores/editor'
import { useSessionStore } from '@/stores/session'
import { getContent, saveContent, type FileContent } from './api'
import { deleteDraft, loadDraft, saveDraft } from './drafts'
import { formatWithPrettier, prettierSupports } from './formatting'
import { detectLanguage } from './languages'
import type { MonacoApi } from './monaco'
import { disposeModel, getModel, isDirty, markSaved, setModel } from './models'
import { editorPrefsSnapshot } from './prefs'
import {
  getVersionContent,
  listVersions,
  restoreVersion as apiRestoreVersion,
  type FileVersion,
} from './versionsApi'

export type CloseRequest = { paths: string[]; then?: (() => void) | undefined }

/** What the diff dialog shows (Step 16b): server vs yours, current vs draft,
 *  a stored version vs the live model, or an editable merge of a conflict. */
export interface DiffSpec {
  path: string
  original: string
  modified: string
  kind: 'conflict' | 'draft' | 'version' | 'merge'
  /** Left/right captions (kind-specific defaults when omitted). */
  leftLabel?: string
  rightLabel?: string
  /** Called with the (edited) right side when the user applies a merge. */
  onApply?: ((modified: string) => void | Promise<void>) | undefined
}

export interface ControllerUI {
  setCloseRequest: (req: CloseRequest | null) => void
  setDiff: (diff: DiffSpec | null) => void
  /** Version history dialog for a live path (Step 16b). */
  openVersions?: (path: string) => void
  /** "Reopen with encoding" picker (Monaco action / F1). */
  openEncodingPicker?: (path: string) => void
  /** Find in files panel in the explorer (Ctrl+Shift+F). */
  openFindInFiles?: (seed?: string) => void
}

/** Tab key of a read-only version tab and its live path. */
export const versionTabKey = (path: string, id: string) => `${path}#v${id}`
export const livePathOf = (key: string) => key.split('#v')[0] ?? key

export class WorkspaceController {
  monaco: MonacoApi
  editorRef: MutableRefObject<Monaco.editor.IStandaloneCodeEditor | null> = { current: null }
  private ui: ControllerUI
  private draftTimers = new Map<string, ReturnType<typeof setTimeout>>()
  private loading = new Map<string, AbortController>()

  constructor(monaco: MonacoApi, ui: ControllerUI) {
    this.monaco = monaco
    this.ui = ui
  }

  private get store() {
    return useEditorStore.getState()
  }

  private get userId(): string {
    return useSessionStore.getState().user?.id ?? 'anon'
  }

  /** The host registers its Monaco instance here (actions/focus/positions). */
  attachEditor(editor: Monaco.editor.IStandaloneCodeEditor): void {
    this.editorRef.current = editor
  }

  detachEditor(editor: Monaco.editor.IStandaloneCodeEditor): void {
    if (this.editorRef.current === editor) this.editorRef.current = null
  }

  tab(path: string): EditorTab | undefined {
    return this.store.tabs.find((t) => t.path === path)
  }

  /** Open (or focus) a tab and start loading it. */
  openFile(path: string, opts: { line?: number; col?: number; activate?: boolean } = {}): boolean {
    const existed = !!this.tab(path)
    const ok = this.store.open(path, opts)
    if (!ok) {
      toast.error(i18n.t('editor:tabs.limit', { max: MAX_EDITOR_TABS }))
      return false
    }
    if (!existed) void this.load(path)
    return true
  }

  /** Fetch the content into the tab's model (fresh model on first load). */
  async load(path: string, q: { encoding?: string; tail?: number } = {}): Promise<void> {
    const prev = this.loading.get(path)
    prev?.abort()
    const ac = new AbortController()
    this.loading.set(path, ac)
    const tab = this.tab(path)
    this.store.patch(path, { status: 'loading', error: null })
    const encoding =
      q.encoding ??
      (tab?.encoding && tab.encoding !== tab.detectedEncoding ? tab.encoding : undefined)
    let c: FileContent
    try {
      c = tab?.versionId
        ? await getVersionContent(tab.versionId, encoding, ac.signal)
        : await getContent(
            path,
            { ...(encoding ? { encoding } : {}), ...(q.tail ? { tail: q.tail } : {}) },
            ac.signal,
          )
    } catch (e) {
      if (ac.signal.aborted) return
      if (e instanceof ApiError && e.code === 'FILE_TOO_LARGE' && !q.tail) {
        // SPEC §8.13: beyond the limit → tail mode, read-only
        return this.load(path, { ...q, tail: 1000 })
      }
      if (e instanceof ApiError && e.code === 'BINARY_FILE') {
        this.store.patch(path, { status: 'binary', error: { code: e.code, message: e.message } })
        return
      }
      if (e instanceof ApiError && e.code === 'NOT_FOUND' && !tab?.etag && !tab?.dirty) {
        // a tab restored from the session (never loaded in this app run)
        // whose file is gone: SPEC §8.13 restores tabs only for files that
        // still exist (review D) — drop it instead of parking an error tab
        toast.message(i18n.t('editor:toast.gone', { name: tab?.name ?? path.split('/').pop() }))
        this.closeNow(path)
        return
      }
      const code = e instanceof ApiError ? e.code : 'NETWORK'
      this.store.patch(path, { status: 'error', error: { code, message: errorMessage(e) } })
      return
    } finally {
      if (this.loading.get(path) === ac) this.loading.delete(path)
    }
    if (ac.signal.aborted) return
    this.applyContent(path, c)
    if (!c.readOnly) await this.checkDraft(path, c)
    if (!tab?.versionId) void this.refreshVersionCount(path)
  }

  private applyContent(path: string, c: FileContent): void {
    const monaco = this.monaco
    const existing = getModel(path)
    const language =
      this.tab(path)?.language !== 'plaintext' && this.tab(path)?.status === 'ready'
        ? this.tab(path)!.language
        : detectLanguage(
            c.path.split('/').pop() ?? '',
            c.content.slice(0, 200).split('\n')[0] ?? '',
          )
    if (existing) {
      existing.setValue(c.content)
      markSaved(path)
      monaco.editor.setModelLanguage(existing, language)
    } else {
      const model = monaco.editor.createModel(
        c.content,
        language,
        monaco.Uri.from({ scheme: 'fm', path }),
      )
      setModel(path, model)
      model.onDidChangeContent(() => this.onModelChange(path))
    }
    this.store.patch(path, {
      status: 'ready',
      error: null,
      language,
      encoding: c.encoding,
      detectedEncoding: c.detectedEncoding,
      bom: c.bom,
      eol: c.eol,
      lossy: c.lossy,
      readOnly: c.readOnly,
      truncated: c.truncated,
      tailLines: c.tailLines ?? 0,
      size: c.size,
      mtime: c.mtime,
      etag: c.etag,
      lines: c.lines,
      dirty: false,
      conflict: null,
    })
  }

  private async checkDraft(path: string, c: FileContent): Promise<void> {
    const d = await loadDraft(this.userId, path)
    if (!d) return
    if (d.content === c.content) {
      await deleteDraft(this.userId, path)
      return
    }
    this.store.patch(path, { draft: d })
  }

  private onModelChange(path: string): void {
    const dirty = isDirty(path)
    const tab = this.tab(path)
    if (tab?.versionId) return
    if (tab && tab.dirty !== dirty) this.store.patch(path, { dirty })
    const timer = this.draftTimers.get(path)
    if (timer) clearTimeout(timer)
    this.draftTimers.set(
      path,
      setTimeout(() => {
        this.draftTimers.delete(path)
        void this.persistDraft(path)
      }, 2000),
    )
  }

  private async persistDraft(path: string): Promise<void> {
    const model = getModel(path)
    const tab = this.tab(path)
    if (!model || !tab) return
    if (!isDirty(path)) {
      await deleteDraft(this.userId, path)
      return
    }
    await saveDraft(this.userId, path, {
      content: model.getValue(),
      etag: tab.etag,
      at: Date.now(),
    })
  }

  /** Save one tab. Returns true when the file is (already) in sync. */
  async save(
    path: string,
    opts: { force?: boolean; encoding?: string; bom?: boolean } = {},
  ): Promise<boolean> {
    const tab = this.tab(path)
    const model = getModel(path)
    if (!tab || !model || tab.status !== 'ready') return false
    if (tab.readOnly) {
      toast.error(i18n.t('editor:toast.readOnly'))
      return false
    }
    if (tab.saving) return false
    const encoding = opts.encoding ?? tab.encoding
    const bom = opts.bom ?? (encoding === tab.encoding ? tab.bom : encoding.startsWith('utf-16'))
    this.store.patch(path, { saving: true })
    try {
      const res = await saveContent({
        path,
        content: model.getValue(),
        encoding,
        bom,
        eol: 'keep',
        etag: tab.etag,
        force: !!opts.force,
      })
      markSaved(path)
      this.store.patch(path, {
        saving: false,
        dirty: false,
        etag: res.etag,
        mtime: res.mtime,
        size: res.size,
        encoding,
        bom,
        conflict: null,
        draft: null,
        eol: model.getEOL() === '\r\n' ? 'crlf' : 'lf',
        lossy: encoding === tab.encoding ? tab.lossy : false,
      })
      const timer = this.draftTimers.get(path)
      if (timer) clearTimeout(timer)
      this.draftTimers.delete(path)
      await deleteDraft(this.userId, path)
      if (res.noop) toast.message(i18n.t('editor:toast.noop', { name: tab.name }))
      else toast.success(i18n.t('editor:toast.saved', { name: tab.name }))
      if (res.version) this.store.patch(path, { versions: (tab.versions ?? 0) + 1 })
      else if (!res.noop) void this.refreshVersionCount(path)
      return true
    } catch (e) {
      this.store.patch(path, { saving: false })
      if (e instanceof ApiError && e.code === 'CONFLICT') {
        const d = e.details as { serverEtag?: string; serverMtime?: string }
        this.store.patch(path, {
          conflict: { serverEtag: d.serverEtag ?? '', serverMtime: d.serverMtime ?? '' },
        })
        return false
      }
      if (
        e instanceof ApiError &&
        e.code === 'VALIDATION_FAILED' &&
        e.fields.content === 'unencodable'
      ) {
        const d = e.details as { line?: number; col?: number; char?: string; encoding?: string }
        toast.error(
          i18n.t('editor:toast.unencodable', {
            char: d.char ?? '?',
            line: d.line ?? 0,
            col: d.col ?? 0,
            enc: d.encoding ?? encoding,
          }),
        )
        if (d.line)
          this.editorRef.current?.revealPositionInCenter({ lineNumber: d.line, column: d.col ?? 1 })
        return false
      }
      toast.error(i18n.t('editor:toast.saveFailed', { name: tab.name, message: errorMessage(e) }))
      return false
    }
  }

  async saveAll(): Promise<void> {
    const dirty = this.store.tabs.filter((t) => t.dirty && !t.readOnly && t.status === 'ready')
    let n = 0
    for (const t of dirty) if (await this.save(t.path)) n++
    if (dirty.length > 1 && n === dirty.length)
      toast.success(i18n.t('editor:toast.savedAll', { n }))
  }

  /** Close with the dirty check (dialog through the UI). */
  requestClose(paths: string[], then?: () => void): void {
    const dirty = paths.filter((p) => this.tab(p)?.dirty)
    if (dirty.length === 0) {
      for (const p of paths) this.closeNow(p)
      then?.()
      return
    }
    this.ui.setCloseRequest({ paths, then })
  }

  closeNow(path: string): void {
    this.loading.get(path)?.abort()
    const timer = this.draftTimers.get(path)
    if (timer) clearTimeout(timer)
    this.draftTimers.delete(path)
    disposeModel(path)
    this.store.close(path)
  }

  /** Resolve a close request: save (then close), discard (drop draft, close) or cancel. */
  async resolveClose(req: CloseRequest, action: 'save' | 'discard' | 'cancel'): Promise<void> {
    this.ui.setCloseRequest(null)
    if (action === 'cancel') return
    for (const p of req.paths) {
      if (action === 'save' && this.tab(p)?.dirty) {
        const ok = await this.save(p)
        if (!ok) return
      } else if (action === 'discard') {
        await deleteDraft(this.userId, p)
      }
      this.closeNow(p)
    }
    req.then?.()
  }

  closeOthers(path: string): void {
    this.requestClose(this.store.tabs.filter((t) => t.path !== path).map((t) => t.path))
  }

  closeRight(path: string): void {
    const i = this.store.tabs.findIndex((t) => t.path === path)
    if (i < 0) return
    this.requestClose(this.store.tabs.slice(i + 1).map((t) => t.path))
  }

  closeAll(): void {
    this.requestClose(this.store.tabs.map((t) => t.path))
  }

  setEOL(path: string, eol: 'lf' | 'crlf'): void {
    const model = getModel(path)
    if (!model) return
    model.setEOL(
      eol === 'crlf'
        ? this.monaco.editor.EndOfLineSequence.CRLF
        : this.monaco.editor.EndOfLineSequence.LF,
    )
    this.store.patch(path, { eol })
  }

  setLanguage(path: string, language: string): void {
    const model = getModel(path)
    if (model) this.monaco.editor.setModelLanguage(model, language)
    this.store.patch(path, { language })
  }

  setIndent(path: string, opts: { insertSpaces?: boolean; tabSize?: number }): void {
    getModel(path)?.updateOptions(opts)
    this.editorRef.current?.layout()
  }

  /** Reopen with another encoding: dirty content is kept as a draft first. */
  async reopenWithEncoding(path: string, encoding: string): Promise<void> {
    if (isDirty(path)) await this.persistDraft(path)
    await this.load(path, { encoding })
    this.store.patch(path, { encoding })
  }

  saveWithEncoding(path: string, encoding: string, bom: boolean): Promise<boolean> {
    return this.save(path, { encoding, bom })
  }

  restoreDraft(path: string): void {
    const tab = this.tab(path)
    const model = getModel(path)
    if (!tab?.draft || !model) return
    model.setValue(tab.draft.content)
    this.store.patch(path, { draft: null, dirty: isDirty(path) })
  }

  async discardDraft(path: string): Promise<void> {
    await deleteDraft(this.userId, path)
    this.store.patch(path, { draft: null })
  }

  async viewDraftDiff(path: string): Promise<void> {
    const tab = this.tab(path)
    const model = getModel(path)
    if (!tab?.draft || !model) return
    this.ui.setDiff({
      path,
      original: model.getValue(),
      modified: tab.draft.content,
      kind: 'draft',
    })
  }

  async resolveConflict(
    path: string,
    action: 'overwrite' | 'reload' | 'diff' | 'merge',
  ): Promise<void> {
    const model = getModel(path)
    if (!model) return
    switch (action) {
      case 'overwrite':
        await this.save(path, { force: true })
        return
      case 'reload':
        await this.persistDraft(path)
        this.store.patch(path, { conflict: null })
        await this.load(path)
        return
      case 'diff':
      case 'merge': {
        try {
          const server = await getContent(path)
          this.store.patch(path, { conflict: null })
          this.ui.setDiff({
            path,
            original: server.content,
            modified: model.getValue(),
            kind: action === 'merge' ? 'merge' : 'conflict',
            onApply:
              action === 'merge'
                ? async (merged) => {
                    model.setValue(merged)
                    await this.save(path, { force: true })
                  }
                : undefined,
          })
        } catch (e) {
          toast.error(errorMessage(e))
        }
      }
    }
  }

  // --- versions (Step 16b) ---

  async refreshVersionCount(path: string): Promise<void> {
    try {
      const res = await listVersions(path)
      if (this.tab(path)) this.store.patch(path, { versions: res.items.length })
    } catch {
      /* count stays unknown */
    }
  }

  /** Open a stored version in a read-only tab. */
  openVersion(path: string, v: FileVersion): boolean {
    const key = versionTabKey(path, v.id)
    const existed = !!this.tab(key)
    const ok = this.store.open(key, { version: { id: v.id, no: v.no } })
    if (!ok) {
      toast.error(i18n.t('editor:tabs.limit', { max: MAX_EDITOR_TABS }))
      return false
    }
    if (!existed) void this.load(key)
    return true
  }

  /** Diff a stored version (left) against the live model or file (right). */
  async compareVersion(path: string, v: FileVersion): Promise<void> {
    try {
      const [old, current] = await Promise.all([
        getVersionContent(v.id),
        (async () => getModel(path)?.getValue() ?? (await getContent(path)).content)(),
      ])
      this.ui.setDiff({
        path,
        original: old.content,
        modified: current,
        kind: 'version',
        leftLabel: i18n.t('editor:dialog.diffVersionLeft', {
          no: v.no,
          at: new Date(v.createdAt).toLocaleString(i18n.resolvedLanguage ?? 'id'),
        }),
      })
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  /** Restore a version; an open tab of the path reloads (unsaved edits go to the draft). */
  async restoreVersion(path: string, v: FileVersion): Promise<boolean> {
    try {
      const res = await apiRestoreVersion(v.id)
      const tab = this.tab(path)
      if (tab) {
        if (isDirty(path)) await this.persistDraft(path)
        this.store.patch(path, { conflict: null })
        await this.load(path)
        toast.success(i18n.t('editor:toast.restoredReload', { no: v.no }))
      } else {
        toast.success(i18n.t('editor:versions.restored', { no: v.no, name: path.split('/').pop() }))
      }
      void res
      return true
    } catch (e) {
      toast.error(errorMessage(e))
      return false
    }
  }

  openVersions(path: string): void {
    this.ui.openVersions?.(livePathOf(path))
  }

  openEncodingPicker(path: string): void {
    this.ui.openEncodingPicker?.(path)
  }

  openFindInFiles(seed?: string): void {
    this.ui.openFindInFiles?.(seed)
  }

  // --- formatting (Step 16b) ---

  /** Format the active document: invalid JSON is reported and left alone;
   *  Markdown/YAML go through Prettier (lazy) when the preference is on;
   *  everything else uses Monaco's built-in formatter when one exists. */
  async formatDocument(path: string): Promise<void> {
    const tab = this.tab(path)
    const model = getModel(path)
    const ed = this.editorRef.current
    if (!tab || !model || !ed || tab.readOnly) return
    if (tab.language === 'json') {
      try {
        JSON.parse(model.getValue())
      } catch (e) {
        const pos = jsonErrorPosition(model.getValue(), e)
        toast.error(i18n.t('editor:format.invalidJson', pos))
        ed.revealLineInCenter(pos.line)
        ed.setPosition({ lineNumber: pos.line, column: pos.col })
        return
      }
    }
    const prefs = editorPrefsSnapshot()
    if (prefs.format_with_prettier && prettierSupports(tab.language)) {
      try {
        const out = await formatWithPrettier(model.getValue(), tab.language, {
          tabWidth: prefs.tab_size,
          useTabs: !prefs.insert_spaces,
        })
        if (out !== model.getValue()) {
          ed.pushUndoStop()
          ed.executeEdits('prettier', [{ range: model.getFullModelRange(), text: out }])
          ed.pushUndoStop()
        }
        toast.message(i18n.t('editor:format.done'))
      } catch (e) {
        toast.error(i18n.t('editor:format.failed', { message: errorMessage(e) }))
      }
      return
    }
    const action = ed.getAction('editor.action.formatDocument')
    const providers = this.monaco.languages.getLanguages().some((l) => l.id === tab.language)
    if (!action || !providers || !hasFormatter(this.monaco, model)) {
      toast.message(i18n.t('editor:format.noFormatter', { language: tab.language }))
      return
    }
    ed.focus()
    await action.run()
  }

  togglePreview(path: string): void {
    const tab = this.tab(path)
    if (!tab) return
    this.store.patch(path, { preview: !tab.preview })
  }

  clearConflict(path: string): void {
    this.store.patch(path, { conflict: null })
  }

  loadMoreTail(path: string): void {
    const tab = this.tab(path)
    if (!tab) return
    void this.load(path, { tail: (tab.tailLines || 1000) + 1000 })
  }

  async download(path: string): Promise<void> {
    try {
      const u = await signUrl(path, 'download')
      startDownload(u.url, path.split('/').pop())
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  runEditorAction(id: string): void {
    const ed = this.editorRef.current
    if (!ed) return
    ed.focus()
    void ed.getAction(id)?.run()
  }

  dispose(): void {
    for (const t of this.draftTimers.values()) clearTimeout(t)
    this.draftTimers.clear()
    for (const ac of this.loading.values()) ac.abort()
    this.loading.clear()
  }
}

function jsonErrorPosition(text: string, e: unknown): { line: number; col: number } {
  const msg = e instanceof Error ? e.message : String(e)
  const m = /position (\d+)/.exec(msg)
  const lineCol = /line (\d+) column (\d+)/.exec(msg)
  if (lineCol) return { line: Number(lineCol[1]), col: Number(lineCol[2]) }
  if (m) {
    const off = Math.min(Number(m[1]), text.length)
    const before = text.slice(0, off)
    const line = before.split('\n').length
    const col = off - before.lastIndexOf('\n')
    return { line, col }
  }
  return { line: 1, col: 1 }
}

/** Whether Monaco has a document formatting provider for the model's language. */
function hasFormatter(_monaco: MonacoApi, model: Monaco.editor.ITextModel): boolean {
  const lang = model.getLanguageId()
  return ['json', 'css', 'scss', 'less', 'html', 'javascript', 'typescript'].includes(lang)
}

export const WorkspaceContext = createContext<WorkspaceController | null>(null)

export function useWorkspace(): WorkspaceController {
  const c = useContext(WorkspaceContext)
  if (!c) throw new Error('useWorkspace outside EditorWorkspace')
  return c
}
