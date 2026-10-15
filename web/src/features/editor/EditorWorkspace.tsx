import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import { PageSpinner } from '@/components/PageSpinner'
import { pushScope, registerShortcut } from '@/lib/shortcuts'
import { useActiveTab, useEditorStore } from '@/stores/editor'
import { useSessionStore } from '@/stores/session'
import { getEditorSession, putEditorSession, type SessionTab } from './api'
import { WorkspaceContext, WorkspaceController, type CloseRequest } from './controller'
import { pruneDrafts } from './drafts'
import { CloseDirtyDialog, ConflictDialog, DiffDialog, type DiffSpec } from './EditorDialogs'
import { EditorPane } from './EditorPane'
import { Explorer } from './Explorer'
import { loadMonaco, type MonacoApi } from './monaco'
import { setEditorPref, useEditorPrefs } from './prefs'
import { QuickOpen } from './QuickOpen'
import { SettingsDialog } from './SettingsDialog'
import { StatusBar } from './StatusBar'
import { TabBar } from './TabBar'
import { Toolbar } from './Toolbar'

export interface EditorWorkspaceProps {
  mode: 'page' | 'modal'
  /** File to open on mount (route `?path=` or the browser's "Buka"). */
  initialPath?: string | undefined
  line?: number | undefined
  col?: number | undefined
  onMaximize?: (() => void) | undefined
  onClose?: (() => void) | undefined
}

const SESSION_DEBOUNCE_MS = 2000

/** The editor workspace (SPEC §8.13, §9.3): toolbar · explorer · tabs ·
 *  Monaco · status bar. Monaco loads lazily; the tab session is restored
 *  from the server and saved 2 s after changes; drafts live in IndexedDB. */
export function EditorWorkspace(p: EditorWorkspaceProps) {
  const { t } = useTranslation('editor')
  const [monaco, setMonaco] = useState<MonacoApi | null>(null)
  const [closeReq, setCloseReq] = useState<CloseRequest | null>(null)
  const [diff, setDiff] = useState<DiffSpec | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [quickOpen, setQuickOpen] = useState(false)
  const [reveal, setReveal] = useState<string | null>(null)
  const prefs = useEditorPrefs()
  const explorerOpen = p.mode === 'modal' ? prefs.explorer && false : prefs.explorer
  const [modalExplorer, setModalExplorer] = useState(false)
  const showExplorer = p.mode === 'modal' ? modalExplorer : explorerOpen
  const navigate = useNavigate()
  const tabs = useEditorStore((s) => s.tabs)
  const activePath = useEditorStore((s) => s.activePath)
  const hydrated = useEditorStore((s) => s.hydrated)
  const active = useActiveTab()

  useEffect(() => {
    let alive = true
    loadMonaco()
      .then((m) => alive && setMonaco(m))
      .catch((e: unknown) => toast.error(e instanceof Error ? e.message : String(e)))
    return () => {
      alive = false
    }
  }, [])

  const ui = useMemo(() => ({ setCloseRequest: setCloseReq, setDiff }), [])
  const ws = useMemo(() => (monaco ? new WorkspaceController(monaco, ui) : null), [monaco, ui])
  useEffect(() => () => ws?.dispose(), [ws])

  // session restore (once per app load) + initial path
  const openedInitial = useRef<string | null>(null)
  useEffect(() => {
    if (!ws) return
    let cancelled = false
    const run = async () => {
      if (!useEditorStore.getState().hydrated) {
        try {
          const s = await getEditorSession()
          if (cancelled) return
          useEditorStore.getState().hydrate(
            s.tabs.map((x) => ({
              path: x.path,
              cursor: x.cursor ?? { line: 1, col: 1 },
              scrollTop: x.scrollTop ?? 0,
              language: x.language ?? 'plaintext',
              encoding: x.encoding ?? 'utf-8',
            })),
            s.activePath,
          )
        } catch {
          useEditorStore.getState().hydrate([], null)
        }
      }
      // restored tabs have no content yet
      for (const tab of useEditorStore.getState().tabs)
        if (tab.status === 'loading' && !tab.etag) void ws.load(tab.path)
      const key = `${p.initialPath ?? ''}|${p.line ?? ''}|${p.col ?? ''}`
      if (p.initialPath && openedInitial.current !== key) {
        openedInitial.current = key
        ws.openFile(p.initialPath, {
          ...(p.line ? { line: p.line } : {}),
          ...(p.col ? { col: p.col } : {}),
        })
      }
      void pruneDrafts()
    }
    void run()
    return () => {
      cancelled = true
    }
  }, [ws, p.initialPath, p.line, p.col])

  // session persistence (debounced)
  const persistTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastSaved = useRef('')
  useEffect(() => {
    if (!hydrated) return
    const session = {
      tabs: tabs.map<SessionTab>((x) => ({
        path: x.path,
        scrollTop: x.scrollTop,
        cursor: x.cursor,
        language: x.language,
        encoding: x.encoding,
      })),
      activePath,
    }
    const json = JSON.stringify(session)
    if (json === lastSaved.current) return
    if (persistTimer.current) clearTimeout(persistTimer.current)
    persistTimer.current = setTimeout(() => {
      lastSaved.current = json
      putEditorSession(session).catch(() => {})
    }, SESSION_DEBOUNCE_MS)
    // a navigation before the debounce fires must not lose the tab list
    const flush = () => {
      if (lastSaved.current === json) return
      lastSaved.current = json
      const csrf = useSessionStore.getState().csrfToken
      void fetch('/api/v1/me/editor-session', {
        method: 'PUT',
        credentials: 'same-origin',
        keepalive: true,
        headers: { 'Content-Type': 'application/json', ...(csrf ? { 'X-CSRF-Token': csrf } : {}) },
        body: json,
      })
    }
    window.addEventListener('pagehide', flush)
    return () => {
      window.removeEventListener('pagehide', flush)
      if (persistTimer.current) clearTimeout(persistTimer.current)
    }
  }, [tabs, activePath, hydrated])

  // auto-save to server (pref, off by default)
  useEffect(() => {
    if (!ws || !prefs.auto_save) return
    const id = setInterval(() => {
      for (const tab of useEditorStore.getState().tabs)
        if (tab.dirty && !tab.conflict && !tab.readOnly) void ws.save(tab.path)
    }, prefs.auto_save_interval * 1000)
    return () => clearInterval(id)
  }, [ws, prefs.auto_save, prefs.auto_save_interval])

  // shortcuts (scope editor; they also fire inside Monaco's textarea)
  useEffect(() => {
    if (!ws) return
    const pop = pushScope('editor')
    const st = () => useEditorStore.getState()
    const offs = [
      registerShortcut('editorSave', () => {
        const a = st().activePath
        if (a) void ws.save(a)
      }),
      registerShortcut('editorSaveAll', () => void ws.saveAll()),
      registerShortcut('editorCloseTab', () => {
        const a = st().activePath
        if (a) ws.requestClose([a])
      }),
      registerShortcut('editorGoToLine', () => ws.runEditorAction('editor.action.gotoLine')),
      registerShortcut('editorPalette', () => ws.runEditorAction('editor.action.quickCommand')),
      registerShortcut('editorQuickOpen', () => setQuickOpen(true)),
      registerShortcut('editorFindInFiles', () => {
        const a = st().activePath
        const dir = a ? a.slice(0, a.lastIndexOf('/')) || '/' : '/'
        void navigate({ to: '/search', search: { kind: 'content', path: dir } })
      }),
      registerShortcut('editorFormat', () => ws.runEditorAction('editor.action.formatDocument')),
    ]
    return () => {
      offs.forEach((off) => off())
      pop()
    }
  }, [ws, navigate])
  useEffect(() => {
    if (!closeReq && !diff && !settingsOpen && !quickOpen) return
    return pushScope('dialog')
  }, [closeReq, diff, settingsOpen, quickOpen])

  const copyPath = useCallback(
    (path: string) => {
      void navigator.clipboard?.writeText(path)
      toast.message(t('toast.pathCopied'))
    },
    [t],
  )
  const toggleExplorer = useCallback(() => {
    if (p.mode === 'modal') setModalExplorer((v) => !v)
    else setEditorPref('explorer', !prefs.explorer)
  }, [p.mode, prefs.explorer])

  if (!ws) return <PageSpinner />
  return (
    <WorkspaceContext.Provider value={ws}>
      <div className="flex h-full min-h-0 flex-col bg-bg" data-testid="editor-workspace">
        <Toolbar
          tab={active}
          mode={p.mode}
          explorerOpen={showExplorer}
          onToggleExplorer={toggleExplorer}
          onSettings={() => setSettingsOpen(true)}
          onMaximize={p.onMaximize}
          onClose={p.onClose}
        />
        <div className="flex min-h-0 flex-1">
          {showExplorer ? <Explorer reveal={reveal} /> : null}
          <div className="flex min-w-0 flex-1 flex-col">
            <TabBar
              tabs={tabs}
              activePath={activePath}
              onActivate={(path) => useEditorStore.getState().activate(path)}
              onClose={(path) => ws.requestClose([path])}
              onCloseOthers={(path) => ws.closeOthers(path)}
              onCloseRight={(path) => ws.closeRight(path)}
              onCloseAll={() => ws.closeAll()}
              onReorder={(from, to) => useEditorStore.getState().reorder(from, to)}
              onCopyPath={copyPath}
              onReveal={(path) => {
                if (p.mode === 'modal') setModalExplorer(true)
                else setEditorPref('explorer', true)
                setReveal(path)
              }}
            />
            <div className="min-h-0 flex-1">
              {active ? (
                <EditorPane key={active.path} tab={active} />
              ) : (
                <div
                  className="flex h-full items-center justify-center p-6 text-center text-sm text-text-muted"
                  data-testid="editor-empty"
                >
                  {t('toolbar.noTab')}
                </div>
              )}
            </div>
            <StatusBar tab={active} />
          </div>
        </div>
        <CloseDirtyDialog req={closeReq} />
        <ConflictDialog tab={active} />
        <DiffDialog spec={diff} monaco={monaco!} onClose={() => setDiff(null)} />
        <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} />
        <QuickOpen
          open={quickOpen}
          onOpenChange={setQuickOpen}
          onPick={(path) => ws.openFile(path)}
        />
      </div>
    </WorkspaceContext.Provider>
  )
}
