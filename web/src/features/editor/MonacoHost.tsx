import { useEffect, useRef } from 'react'
import type * as Monaco from 'monaco-editor'
import i18n from '@/i18n'
import { useUIStore } from '@/stores/ui'
import { useEditorStore, type EditorTab } from '@/stores/editor'
import { useWorkspace } from './controller'
import { getModel, saveViewState, takeViewState } from './models'
import { monacoTheme } from './monaco'
import { EDITOR_FONTS, useEditorPrefs } from './prefs'

/** One Monaco instance for the workspace; the model is swapped when the
 *  active tab changes (view state saved/restored per path). Preferences
 *  map to editor options; the theme follows the app unless overridden. */
export function MonacoHost({ tab }: { tab: EditorTab }) {
  const ws = useWorkspace()
  const monaco = ws.monaco
  const prefs = useEditorPrefs()
  const resolved = useUIStore((s) => s.resolvedTheme)
  const hostRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null)
  const pathRef = useRef<string | null>(null)

  // create once
  useEffect(() => {
    if (!hostRef.current) return
    const editor = monaco.editor.create(hostRef.current, {
      model: null,
      automaticLayout: true,
      scrollBeyondLastLine: false,
      fontFamily: fontStack(prefs.font_family),
      fontSize: prefs.font_size,
      fontLigatures: false,
      tabSize: prefs.tab_size,
      insertSpaces: prefs.insert_spaces,
      wordWrap: prefs.word_wrap ? 'on' : 'off',
      minimap: { enabled: prefs.minimap },
      renderWhitespace: prefs.render_whitespace,
      lineNumbers: prefs.line_numbers ? 'on' : 'off',
      bracketPairColorization: { enabled: prefs.bracket_pair_colorization },
      multiCursorModifier: 'ctrlCmd',
      folding: true,
      matchBrackets: 'always',
      theme: monacoTheme(prefs.theme, resolved),
      ariaLabel: 'editor',
      padding: { top: 6 },
    })
    editorRef.current = editor
    ws.attachEditor(editor)
    // application commands in Monaco's F1 palette (SPEC §8.13 "F1 command palette")
    const act = (id: string, label: string, run: () => void, keybindings?: number[]) =>
      editor.addAction({
        id,
        label,
        run,
        contextMenuGroupId: 'fm',
        ...(keybindings ? { keybindings } : {}),
      })
    const t = (k: string): string => String((i18n.t as (key: string) => unknown)(`editor:${k}`))
    const activePath = () => useEditorStore.getState().activePath
    act('fm.saveAll', t('toolbar.saveAll'), () => void ws.saveAll())
    act('fm.closeOthers', t('tabs.closeOthers'), () => {
      const a = activePath()
      if (a) ws.closeOthers(a)
    })
    act('fm.togglePreview', t('toolbar.preview'), () => {
      const a = activePath()
      if (a) ws.togglePreview(a)
    })
    act('fm.reopenWithEncoding', t('encoding.reopen'), () => {
      const a = activePath()
      if (a) ws.openEncodingPicker(a)
    })
    act('fm.versionHistory', t('toolbar.history'), () => {
      const a = activePath()
      if (a) ws.openVersions(a)
    })
    act('fm.findInFiles', t('toolbar.findInFiles'), () => {
      const sel = editor.getModel()?.getValueInRange(editor.getSelection()!) ?? ''
      ws.openFindInFiles(sel.includes('\n') ? '' : sel)
    })
    act('fm.formatDocument', t('toolbar.format'), () => {
      const a = activePath()
      if (a) void ws.formatDocument(a)
    })
    // Shift+Alt+F inside Monaco would run its own formatter (no JSON validity
    // check, no Prettier): route it through the workspace instead
    const keys = editor.onKeyDown((e) => {
      if (e.shiftKey && e.altKey && !e.ctrlKey && e.keyCode === monaco.KeyCode.KeyF) {
        e.preventDefault()
        e.stopPropagation()
        const a = activePath()
        if (a) void ws.formatDocument(a)
      }
    })
    const cursor = editor.onDidChangeCursorPosition((e) => {
      const p = pathRef.current
      if (!p) return
      useEditorStore
        .getState()
        .patch(p, { cursor: { line: e.position.lineNumber, col: e.position.column } })
    })
    let scrollTimer: ReturnType<typeof setTimeout> | null = null
    const scroll = editor.onDidScrollChange((e) => {
      const p = pathRef.current
      if (!p) return
      if (scrollTimer) clearTimeout(scrollTimer)
      scrollTimer = setTimeout(
        () => useEditorStore.getState().patch(p, { scrollTop: Math.round(e.scrollTop) }),
        300,
      )
    })
    return () => {
      keys.dispose()
      cursor.dispose()
      scroll.dispose()
      if (scrollTimer) clearTimeout(scrollTimer)
      if (pathRef.current) saveViewState(pathRef.current, editor.saveViewState())
      editor.dispose()
      editorRef.current = null
      ws.detachEditor(editor)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- created once; options follow below
  }, [monaco, ws])

  // options from preferences
  useEffect(() => {
    editorRef.current?.updateOptions({
      fontFamily: fontStack(prefs.font_family),
      fontSize: prefs.font_size,
      wordWrap: prefs.word_wrap ? 'on' : 'off',
      minimap: { enabled: prefs.minimap },
      renderWhitespace: prefs.render_whitespace,
      lineNumbers: prefs.line_numbers ? 'on' : 'off',
      bracketPairColorization: { enabled: prefs.bracket_pair_colorization },
    })
  }, [prefs])
  useEffect(() => {
    monaco.editor.setTheme(monacoTheme(prefs.theme, resolved))
  }, [monaco, prefs.theme, resolved])

  // model swap + pending jump
  const pending = tab.pending
  const status = tab.status
  useEffect(() => {
    const editor = editorRef.current
    if (!editor || status !== 'ready') return
    const model = getModel(tab.path)
    if (!model) return
    if (pathRef.current !== tab.path) {
      if (pathRef.current) saveViewState(pathRef.current, editor.saveViewState())
      editor.setModel(model)
      model.updateOptions({ tabSize: prefs.tab_size, insertSpaces: prefs.insert_spaces })
      const vs = takeViewState(tab.path)
      if (vs) editor.restoreViewState(vs)
      else if (tab.cursor.line > 1 || tab.cursor.col > 1 || tab.scrollTop > 0) {
        editor.setPosition({ lineNumber: tab.cursor.line, column: tab.cursor.col })
        editor.setScrollTop(tab.scrollTop)
      }
      pathRef.current = tab.path
    }
    editor.updateOptions({ readOnly: tab.readOnly })
    if (pending) {
      const pos = {
        lineNumber: Math.min(pending.line, model.getLineCount()),
        column: pending.col ?? 1,
      }
      if (pending.len && pending.len > 0) {
        // anchor at the end, cursor at the start: Ln/Col show the match start
        editor.setSelection(
          new monaco.Selection(
            pos.lineNumber,
            pos.column + pending.len,
            pos.lineNumber,
            pos.column,
          ),
        )
      } else editor.setPosition(pos)
      editor.revealPositionInCenter(pos)
      useEditorStore.getState().patch(tab.path, { pending: null })
    }
    editor.focus()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- cursor/scrollTop only seed the first mount of a restored tab
  }, [tab.path, status, pending, tab.readOnly])

  return <div ref={hostRef} data-testid="monaco-host" className="h-full w-full" />
}

function fontStack(family: string): string {
  const safe = (EDITOR_FONTS as readonly string[]).includes(family) ? family : 'JetBrains Mono'
  return `"${safe}", "JetBrains Mono", Consolas, Menlo, "Ubuntu Mono", monospace`
}
