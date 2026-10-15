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
import { detectLanguage } from './languages'
import type { MonacoApi } from './monaco'
import { disposeModel, getModel, isDirty, markSaved, setModel } from './models'

export type CloseRequest = { paths: string[]; then?: (() => void) | undefined }

export interface ControllerUI {
  setCloseRequest: (req: CloseRequest | null) => void
  setDiff: (
    diff: { path: string; original: string; modified: string; kind: 'conflict' | 'draft' } | null,
  ) => void
}

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
      c = await getContent(
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
      const code = e instanceof ApiError ? e.code : 'NETWORK'
      this.store.patch(path, { status: 'error', error: { code, message: errorMessage(e) } })
      return
    } finally {
      if (this.loading.get(path) === ac) this.loading.delete(path)
    }
    if (ac.signal.aborted) return
    this.applyContent(path, c)
    if (!c.readOnly) await this.checkDraft(path, c)
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

  async resolveConflict(path: string, action: 'overwrite' | 'reload' | 'diff'): Promise<void> {
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
      case 'diff': {
        try {
          const server = await getContent(path)
          this.ui.setDiff({
            path,
            original: server.content,
            modified: model.getValue(),
            kind: 'conflict',
          })
        } catch (e) {
          toast.error(errorMessage(e))
        }
      }
    }
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

export const WorkspaceContext = createContext<WorkspaceController | null>(null)

export function useWorkspace(): WorkspaceController {
  const c = useContext(WorkspaceContext)
  if (!c) throw new Error('useWorkspace outside EditorWorkspace')
  return c
}
