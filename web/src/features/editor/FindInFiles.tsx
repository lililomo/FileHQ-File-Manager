import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronRight, Replace, Search, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { FileIcon } from '@/components/file-icon/FileIcon'
import {
  searchContent,
  type ContentMatch,
  type ContentQuery,
  type ContentSummary,
} from '@/features/search/api'
import { Highlight } from '@/features/search/ContentResults'
import { groupMatches } from '@/features/search/grouping'
import { ReplacePanel } from '@/features/search/ReplacePanel'
import { errorMessage } from '@/lib/errors'
import { useEditorStore } from '@/stores/editor'
import { useWorkspace } from './controller'

export interface FindInFilesProps {
  /** Initial query (selection in the editor). */
  seed?: string | undefined
  onClose: () => void
}

/** Find in files panel of the explorer (SPEC §8.13 "Ctrl+Shift+F"): query +
 *  regex/case/whole-word + extension glob, limited to a folder (default: the
 *  active tab's folder); results per file → line; a click opens the tab at
 *  line/column with the match selected. "Replace in files…" reuses the
 *  Step 15 preview/apply panel. */
export function FindInFiles({ seed, onClose }: FindInFilesProps) {
  const { t, i18n } = useTranslation('editor')
  const ws = useWorkspace()
  const activePath = useEditorStore((s) => s.activePath)
  const defaultDir = activePath ? activePath.slice(0, activePath.lastIndexOf('/')) || '/' : '/'
  const [q, setQ] = useState(seed ?? '')
  const [folder, setFolder] = useState(defaultDir)
  const [regex, setRegex] = useState(false)
  const [cs, setCs] = useState(false)
  const [ww, setWw] = useState(false)
  const [ext, setExt] = useState('')
  const [matches, setMatches] = useState<ContentMatch[]>([])
  const [summary, setSummary] = useState<ContentSummary | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [running, setRunning] = useState(false)
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set())
  const [replaceOpen, setReplaceOpen] = useState(false)
  const abort = useRef<AbortController | null>(null)
  useEffect(() => () => abort.current?.abort(), [])
  const [prevSeed, setPrevSeed] = useState(seed)
  if (seed !== prevSeed) {
    setPrevSeed(seed)
    if (seed) setQ(seed)
  }

  const query = (): ContentQuery => ({
    q,
    path: folder || '/',
    regex,
    caseSensitive: cs,
    wholeWord: ww,
    recursive: true,
    ...(ext.trim()
      ? {
          ext: ext
            .split(',')
            .map((x) => x.trim().replace(/^\./, ''))
            .filter(Boolean),
        }
      : {}),
  })
  const run = async () => {
    if (!q.trim()) return
    abort.current?.abort()
    const ctl = new AbortController()
    abort.current = ctl
    setRunning(true)
    setError(null)
    setMatches([])
    setSummary(null)
    const buf: ContentMatch[] = []
    let flush: ReturnType<typeof setTimeout> | null = null
    try {
      const s = await searchContent(
        query(),
        (m) => {
          buf.push(m)
          if (!flush)
            flush = setTimeout(() => {
              flush = null
              setMatches([...buf])
            }, 80)
        },
        ctl.signal,
      )
      if (flush) clearTimeout(flush)
      setMatches([...buf])
      setSummary(s)
    } catch (e) {
      if (!ctl.signal.aborted) setError(errorMessage(e))
    } finally {
      if (abort.current === ctl) setRunning(false)
    }
  }
  const stop = () => {
    abort.current?.abort()
    setRunning(false)
  }
  const groups = groupMatches(matches)
  const toggle = (file: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(file)) next.delete(file)
      else next.add(file)
      return next
    })
  const open = (m: ContentMatch) =>
    ws.openFile(m.file, { line: m.line, col: m.col, ...(m.len ? { len: m.len } : {}) })
  const lang = i18n.resolvedLanguage ?? 'id'
  return (
    <div
      className="flex min-h-0 flex-1 flex-col gap-1 px-2 pb-2 text-xs"
      data-testid="find-in-files"
    >
      <div className="flex items-center gap-1">
        <span className="flex-1 font-medium uppercase tracking-wide text-text-muted">
          {t('find.title')}
        </span>
        <IconButton
          size="icon-xs"
          label={t('find.close')}
          onClick={onClose}
          data-testid="find-close"
        >
          <X />
        </IconButton>
      </div>
      <form
        className="flex flex-col gap-1"
        onSubmit={(e) => {
          e.preventDefault()
          void run()
        }}
      >
        <div className="flex gap-1">
          <Input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t('find.placeholder')}
            aria-label={t('find.placeholder')}
            className="h-7 text-xs"
            data-testid="find-query"
            autoFocus
          />
          {running ? (
            <Button type="button" size="xs" variant="outline" onClick={stop}>
              {t('find.stop')}
            </Button>
          ) : (
            <Button type="submit" size="xs" data-testid="find-run">
              <Search /> {t('find.run')}
            </Button>
          )}
        </div>
        <Input
          value={folder}
          onChange={(e) => setFolder(e.target.value)}
          aria-label={t('find.folder')}
          placeholder={t('find.folder')}
          className="h-7 font-mono text-xs"
          data-testid="find-folder"
        />
        <Input
          value={ext}
          onChange={(e) => setExt(e.target.value)}
          aria-label={t('find.glob')}
          placeholder={t('find.glob')}
          className="h-7 text-xs"
          data-testid="find-ext"
        />
        <div className="flex flex-wrap gap-3">
          <label className="flex items-center gap-1">
            <Checkbox
              checked={regex}
              onCheckedChange={(v) => setRegex(v === true)}
              data-testid="find-regex"
            />{' '}
            {t('find.regex')}
          </label>
          <label className="flex items-center gap-1">
            <Checkbox
              checked={cs}
              onCheckedChange={(v) => setCs(v === true)}
              data-testid="find-case"
            />{' '}
            {t('find.caseSensitive')}
          </label>
          <label className="flex items-center gap-1">
            <Checkbox
              checked={ww}
              onCheckedChange={(v) => setWw(v === true)}
              data-testid="find-word"
            />{' '}
            {t('find.wholeWord')}
          </label>
        </div>
      </form>
      {error ? (
        <p className="text-destructive" role="alert">
          {t('find.error', { message: error })}
        </p>
      ) : null}
      {summary ? (
        <div className="flex items-center gap-2 text-text-muted" data-testid="find-summary">
          <span className="flex-1">
            {t('find.summary', {
              matches: summary.matches.toLocaleString(lang),
              files: summary.files.toLocaleString(lang),
            })}
            {summary.truncated ? ` · ${t('find.truncated', { n: summary.matches })}` : ''}
          </span>
          {summary.matches > 0 ? (
            <Button
              size="xs"
              variant="outline"
              onClick={() => setReplaceOpen(true)}
              data-testid="find-replace"
            >
              <Replace /> {t('find.replace')}
            </Button>
          ) : null}
        </div>
      ) : null}
      <div
        className="min-h-0 flex-1 overflow-auto"
        role="tree"
        aria-label={t('find.title')}
        data-testid="find-results"
      >
        {summary && groups.length === 0 ? (
          <p className="py-2 text-text-muted">{t('find.empty')}</p>
        ) : null}
        {groups.map((g) => {
          const open_ = !collapsed.has(g.file)
          return (
            <div key={g.file} role="treeitem" aria-expanded={open_}>
              <button
                type="button"
                className="flex w-full items-center gap-1 rounded px-1 py-0.5 text-left hover:bg-surface-3"
                onClick={() => toggle(g.file)}
                title={g.file}
                data-testid="find-file"
              >
                {open_ ? (
                  <ChevronDown className="size-3 shrink-0" />
                ) : (
                  <ChevronRight className="size-3 shrink-0" />
                )}
                <FileIcon category="code" size={16} className="shrink-0" />
                <span className="min-w-0 flex-1 truncate">{g.file.split('/').pop()}</span>
                <span className="text-text-muted">{g.matches.length}</span>
              </button>
              {open_ ? (
                <ul role="group" className="ml-4">
                  {g.matches.map((m, i) => (
                    <li key={`${m.line}:${m.col}:${i}`} role="none">
                      <button
                        type="button"
                        role="treeitem"
                        className="flex w-full gap-2 truncate rounded px-1 py-0.5 text-left font-mono hover:bg-surface-3"
                        onClick={() => open(m)}
                        data-testid="find-match"
                        data-line={m.line}
                        data-col={m.col}
                      >
                        <span className="shrink-0 tabular-nums text-text-muted">{m.line}</span>
                        <span className="truncate">
                          <Highlight
                            text={m.text}
                            spans={[{ col: m.col, len: m.len ?? q.length }]}
                          />
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          )
        })}
      </div>
      <Dialog open={replaceOpen} onOpenChange={setReplaceOpen}>
        <DialogContent
          className="flex max-h-[85vh] flex-col sm:max-w-3xl"
          data-testid="find-replace-dialog"
        >
          <DialogHeader>
            <DialogTitle>{t('find.replaceTitle')}</DialogTitle>
          </DialogHeader>
          {replaceOpen ? (
            <ReplacePanel
              query={query()}
              onClose={() => setReplaceOpen(false)}
              onApplied={() => {
                // reload open, clean tabs that live under the folder
                for (const tab of useEditorStore.getState().tabs)
                  if (!tab.dirty && !tab.versionId && tab.status === 'ready') void ws.load(tab.path)
                void run()
              }}
            />
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  )
}
