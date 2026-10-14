import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowUp,
  ChevronRight,
  Download,
  FilePlus2,
  FolderArchive,
  Loader2,
  PackageOpen,
  Trash2,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { BrowserContextMenu } from '@/features/browser/ContextMenu'
import { baseName, parentPath } from '@/features/browser/path'
import type { ConflictPolicy } from '@/features/file-ops/api'
import { errorMessage } from '@/features/file-ops/useFileOps'
import type { Task } from '@/features/tasks/api'
import { taskLabel } from '@/features/tasks/taskLabel'
import { announce } from '@/lib/announce'
import { ApiError } from '@/lib/api'
import type { ActionContext } from '@/lib/actions'
import { formatBytes, formatNumber } from '@/lib/format'
import { startDownload } from '@/lib/media-url'
import { cn } from '@/lib/utils'
import { useTasksStore } from '@/stores/tasks'
import {
  addToArchive,
  archiveEntriesKey,
  extractArchive,
  extractArchiveEntries,
  innerCrumbs,
  innerParent,
  listArchiveEntries,
  NAME_ENCODINGS,
  removeFromArchive,
  signArchiveEntry,
  type ArchiveEntry,
  type NameEncoding,
} from './api'
import { AddFilesDialog } from './AddFilesDialog'
import { EntryPreview } from './EntryPreview'
import { EntryTable } from './EntryTable'
import { sortEntries, type EntrySort } from './entrySort'
import { ExtractEntriesDialog } from './ExtractEntriesDialog'
import { PasswordPrompt } from './PasswordPrompt'

export interface ArchiveBrowserProps {
  /** virtual path of the archive */
  archivePath: string
  onClose: () => void
}

type LooseT = (key: string, options?: Record<string, unknown>) => string
type DialogState =
  | { kind: 'password'; error?: string }
  | { kind: 'extract'; entries: string[] }
  | { kind: 'add' }
  | { kind: 'remove'; entries: string[] }
  | null

const PAGE = 1000

/** Archive browser overlay (SPEC §8.11 "Archive browser", PROMPT Step 14b):
 *  `?archive=<path>` opens an archive like a read-only folder — breadcrumb
 *  inside the archive, virtualised member table, text/image preview
 *  streamed out of the archive, extract all / selected, and for unencrypted
 *  zips add/remove members as tasks. The password is asked once, kept in
 *  component state, sent in a header and turned into a server-side session
 *  id for signed preview URLs. */
export function ArchiveBrowser({ archivePath, onClose }: ArchiveBrowserProps) {
  const { t, i18n } = useTranslation(['archive', 'fileops', 'errors', 'tasks', 'files'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const name = baseName(archivePath)
  const dir = parentPath(archivePath) ?? '/'
  const [inner, setInner] = useState('')
  const [encoding, setEncoding] = useState<NameEncoding>('auto')
  const [password, setPassword] = useState('')
  const [pwVersion, setPwVersion] = useState(0)
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [active, setActive] = useState<string | null>(null)
  const [sort, setSort] = useState<EntrySort>({ field: 'name', dir: 'asc' })
  const [preview, setPreview] = useState<ArchiveEntry | null>(null)
  const [dialog, setDialog] = useState<DialogState>(null)
  const [busy, setBusy] = useState(false)
  const [extra, setExtra] = useState<{ key: string; items: ArchiveEntry[]; next?: string }>({
    key: '',
    items: [],
  })
  const gridHost = useRef<HTMLDivElement>(null)

  const query = useQuery({
    queryKey: [...archiveEntriesKey({ path: archivePath, inner, encoding }), pwVersion],
    queryFn: ({ signal }) =>
      listArchiveEntries(
        { path: archivePath, inner, encoding, limit: PAGE, ...(password ? { password } : {}) },
        signal,
      ),
    retry: false,
    placeholderData: (prev) => prev,
  })
  const data = query.data
  // every listing made with the password carries the (refreshed) session id
  const session = data?.session
  const pageKey = `${archivePath}|${inner}|${encoding}|${pwVersion}|${data?.nextCursor ?? ''}`
  const items = useMemo(() => {
    const base = data?.items ?? []
    const more = extra.key === pageKey ? extra.items : []
    return sortEntries([...base, ...more], sort)
  }, [data, extra, pageKey, sort])
  const nextCursor = extra.key === pageKey ? extra.next : data?.nextCursor
  const byPath = useMemo(() => new Map(items.map((e) => [e.path, e])), [items])
  const selectedEntries = useMemo(
    () => [...selected].map((p) => byPath.get(p)).filter((e): e is ArchiveEntry => !!e),
    [selected, byPath],
  )

  // password handling: 401 ARCHIVE_PASSWORD_REQUIRED / 422 ARCHIVE_PASSWORD_WRONG
  // (derived from the query error; "cancel" remembers which error was dismissed)
  const err = query.error
  const [dismissed, setDismissed] = useState<unknown>(null)
  const pwError =
    err instanceof ApiError &&
    (err.code === 'ARCHIVE_PASSWORD_REQUIRED' || err.code === 'ARCHIVE_PASSWORD_WRONG')
      ? err
      : null
  const askPassword = !!pwError && dismissed !== pwError && dialog === null

  // a finished zip edit of this archive refreshes the listing
  useEffect(() => {
    const seen = new Set<string>()
    return useTasksStore.subscribe((state) => {
      for (const task of Object.values(state.tasks) as Task[]) {
        if (task.type !== 'zip_edit' || task.state !== 'done' || seen.has(task.id)) continue
        const p = task.payload as { archive?: string } | undefined
        if (p?.archive !== archivePath) continue
        seen.add(task.id)
        void qc.invalidateQueries({ queryKey: ['archive-entries', archivePath] })
        setExtra({ key: '', items: [] })
      }
    })
  }, [archivePath, qc])

  const navigateInner = useCallback((to: string) => {
    setInner(to)
    setSelected(new Set())
    setActive(null)
    setPreview(null)
    setExtra({ key: '', items: [] })
  }, [])

  const needPassword = useCallback(
    (entry?: ArchiveEntry) => {
      if (password) return false
      if (entry ? entry.encrypted : data?.encrypted) {
        setDialog({ kind: 'password' })
        return true
      }
      return false
    },
    [password, data?.encrypted],
  )

  const activate = useCallback(
    (entry: ArchiveEntry) => {
      if (entry.isDir) {
        navigateInner(entry.path)
        return
      }
      if (needPassword(entry)) return
      setPreview(entry)
    },
    [navigateInner, needPassword],
  )

  const queueTask = (res: { task: Task }) => {
    const store = useTasksStore.getState()
    store.upsert(res.task)
    store.setPanel(true)
    const msg = t('tasks:toast.queued', { title: taskLabel(res.task, lt, lang) })
    toast.info(msg)
    announce(msg)
  }

  const runExtract = async (entries: string[], dest: string, policy: ConflictPolicy) => {
    setBusy(true)
    try {
      const res =
        entries.length > 0
          ? await extractArchiveEntries({
              path: archivePath,
              entries,
              dest,
              password: password || undefined,
              encoding,
              onConflict: policy,
            })
          : await extractArchive({
              path: archivePath,
              dest,
              password: password || undefined,
              encoding,
              onConflict: policy,
            })
      queueTask(res)
      setDialog(null)
    } catch (e) {
      if (e instanceof ApiError && e.code.startsWith('ARCHIVE_PASSWORD')) {
        setDialog({ kind: 'password', error: password ? t('archive:browser.password.wrong') : '' })
        return
      }
      toast.error(errorMessage(lt, e))
    } finally {
      setBusy(false)
    }
  }

  const runAdd = async (sources: string[]) => {
    setBusy(true)
    try {
      queueTask(await addToArchive(archivePath, sources, inner))
      setDialog(null)
    } catch (e) {
      toast.error(errorMessage(lt, e))
    } finally {
      setBusy(false)
    }
  }

  const runRemove = async (entries: string[]) => {
    setBusy(true)
    try {
      queueTask(await removeFromArchive(archivePath, entries))
      setSelected(new Set())
      setPreview(null)
      setDialog(null)
    } catch (e) {
      toast.error(errorMessage(lt, e))
    } finally {
      setBusy(false)
    }
  }

  const downloadEntry = async (entry: ArchiveEntry) => {
    if (needPassword(entry)) return
    try {
      const signed = await signArchiveEntry(archivePath, entry.path, session)
      startDownload(signed.url, entry.name)
    } catch (e) {
      toast.error(errorMessage(lt, e))
    }
  }

  const canModify = !!data?.supportsModify
  const modifyHint = data?.modifyReason ? t(`archive:browser.modify.${data.modifyReason}`) : ''
  const selectedPaths = selectedEntries.map((e) => e.path)
  const focusGrid = () =>
    gridHost.current?.querySelector<HTMLElement>('[data-testid="archive-grid"]')?.focus()

  const actionCtx = useMemo<ActionContext | null>(() => {
    if (selectedEntries.length === 0) return null
    const noop = () => {}
    const paths = selectedEntries.map((e) => e.path)
    return {
      path: inner,
      selection: [],
      selectionCount: selectedEntries.length,
      total: items.length,
      clipboardCount: 0,
      canUndo: false,
      navigate: noop,
      refresh: () => void query.refetch(),
      openNew: noop,
      openTab: noop,
      closeTab: noop,
      togglePreview: noop,
      toggleTheme: noop,
      selectAll: () => setSelected(new Set(items.map((e) => e.path))),
      invertSelection: noop,
      clearSelection: () => setSelected(new Set()),
      openHelp: noop,
      openColumns: noop,
      openProperties: noop,
      openFolderProperties: noop,
      quickLook: noop,
      focusFilter: noop,
      open: () => {
        const first = selectedEntries[0]
        if (selectedEntries.length === 1 && first) activate(first)
      },
      openInNewTab: noop,
      copy: noop,
      cut: noop,
      paste: noop,
      duplicate: noop,
      rename: noop,
      moveTo: noop,
      copyTo: noop,
      deleteItems: noop,
      deletePermanent: noop,
      batchRename: noop,
      permissions: noop,
      symlinkTo: noop,
      copyPath: noop,
      undo: noop,
      extractEntries: () => {
        if (!needPassword()) setDialog({ kind: 'extract', entries: paths })
      },
      downloadEntry: () => {
        const first = selectedEntries[0]
        if (selectedEntries.length === 1 && first && !first.isDir) void downloadEntry(first)
      },
      removeEntries: canModify ? () => setDialog({ kind: 'remove', entries: paths }) : undefined,
      archiveCanModify: canModify,
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- handlers read the latest state through closures above
  }, [selectedEntries, items, inner, canModify, activate])

  const crumbs = innerCrumbs(inner)
  const totalInFolder = data?.total ?? 0
  const selectedBytes = selectedEntries.reduce((n, e) => n + (e.isDir ? 0 : e.size), 0)

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent
        className="flex h-[92vh] w-[96vw] max-w-6xl flex-col gap-0 p-0 sm:max-w-6xl"
        data-testid="archive-browser"
        onOpenAutoFocus={(e) => {
          e.preventDefault()
          focusGrid()
        }}
      >
        <DialogHeader className="border-b border-border px-4 py-3 pr-12">
          <DialogTitle className="flex items-center gap-2 text-base">
            <FolderArchive aria-hidden="true" className="size-5 shrink-0" />
            <span className="truncate" data-testid="archive-title">
              {name}
            </span>
          </DialogTitle>
          <DialogDescription className="truncate text-xs">
            {data
              ? t('archive:browser.summary', {
                  format: data.format,
                  size: formatBytes(data.size, lang),
                  n: formatNumber(data.totalEntries, lang),
                  count: data.totalEntries,
                })
              : t('archive:browser.loading')}
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-1 border-b border-border px-3 py-1.5 text-sm">
          <nav aria-label={t('archive:browser.breadcrumb')} className="flex min-w-0 items-center">
            <Button
              size="sm"
              variant="ghost"
              className={cn('h-7 px-2', inner === '' && 'font-semibold')}
              onClick={() => navigateInner('')}
              data-testid="archive-crumb-root"
            >
              {name}
            </Button>
            {crumbs.map((c) => (
              <span key={c.inner} className="flex items-center">
                <ChevronRight className="size-3 text-text-muted" aria-hidden="true" />
                <Button
                  size="sm"
                  variant="ghost"
                  className={cn('h-7 px-2', c.inner === inner && 'font-semibold')}
                  onClick={() => navigateInner(c.inner)}
                  aria-current={c.inner === inner ? 'location' : undefined}
                  data-testid="archive-crumb"
                >
                  {c.name}
                </Button>
              </span>
            ))}
          </nav>
          <div className="ml-auto flex flex-wrap items-center gap-1">
            <Button
              size="sm"
              variant="ghost"
              className="h-7"
              disabled={inner === ''}
              onClick={() => navigateInner(innerParent(inner))}
              aria-label={t('archive:browser.actions.up')}
              data-testid="archive-up"
            >
              <ArrowUp aria-hidden="true" />
            </Button>
            <Select
              value={encoding}
              onValueChange={(v) => {
                setEncoding(v as NameEncoding)
                setExtra({ key: '', items: [] })
              }}
            >
              <SelectTrigger
                className="h-7 w-44 text-xs"
                aria-label={t('archive:extract.encoding')}
                data-testid="archive-encoding"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {NAME_ENCODINGS.map((enc) => (
                  <SelectItem key={enc} value={enc}>
                    {t(`archive:encoding.${enc}`)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1 border-b border-border px-3 py-1.5">
          <Button
            size="sm"
            variant="outline"
            disabled={!data || busy}
            onClick={() => {
              if (!needPassword()) setDialog({ kind: 'extract', entries: [] })
            }}
            data-testid="archive-extract-all"
          >
            <PackageOpen aria-hidden="true" />
            {t('archive:browser.actions.extractAll')}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={selectedPaths.length === 0 || busy}
            onClick={() => {
              if (!needPassword()) setDialog({ kind: 'extract', entries: selectedPaths })
            }}
            data-testid="archive-extract-selected"
          >
            <PackageOpen aria-hidden="true" />
            {t('archive:browser.actions.extractSelected', { count: selectedPaths.length })}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={selectedEntries.length !== 1 || !!selectedEntries[0]?.isDir || busy || !data}
            onClick={() => {
              const first = selectedEntries[0]
              if (first) void downloadEntry(first)
            }}
            data-testid="archive-download-entry"
          >
            <Download aria-hidden="true" />
            {t('archive:browser.actions.downloadEntry')}
          </Button>
          <span className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
          <Button
            size="sm"
            variant="outline"
            disabled={!canModify || busy}
            title={canModify ? undefined : modifyHint}
            onClick={() => setDialog({ kind: 'add' })}
            data-testid="archive-add"
          >
            <FilePlus2 aria-hidden="true" />
            {t('archive:browser.actions.add')}
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="text-danger"
            disabled={!canModify || selectedPaths.length === 0 || busy}
            title={canModify ? undefined : modifyHint}
            onClick={() => setDialog({ kind: 'remove', entries: selectedPaths })}
            data-testid="archive-remove"
          >
            <Trash2 aria-hidden="true" />
            {t('archive:browser.actions.remove', { count: selectedPaths.length })}
          </Button>
          {!canModify && modifyHint ? (
            <span className="ml-2 text-xs text-text-muted" data-testid="archive-modify-hint">
              {modifyHint}
            </span>
          ) : null}
        </div>
        <div className="flex min-h-0 flex-1">
          <div ref={gridHost} className="flex min-h-0 min-w-0 flex-1 flex-col">
            {query.isPending && !data ? (
              <div className="flex items-center gap-2 p-4 text-sm text-text-muted" aria-busy="true">
                <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                {t('archive:browser.loading')}
              </div>
            ) : query.isError && !data ? (
              <EmptyState
                title={t('archive:browser.error')}
                description={
                  err instanceof ApiError && err.code.startsWith('ARCHIVE_PASSWORD')
                    ? t('archive:browser.password.needed')
                    : errorMessage(lt, err)
                }
                action={
                  err instanceof ApiError && err.code.startsWith('ARCHIVE_PASSWORD') ? (
                    <Button variant="outline" onClick={() => setDialog({ kind: 'password' })}>
                      {t('archive:browser.password.enter')}
                    </Button>
                  ) : (
                    <Button variant="outline" onClick={() => void query.refetch()}>
                      {t('archive:browser.retry')}
                    </Button>
                  )
                }
                className="h-full"
              />
            ) : items.length === 0 ? (
              <EmptyState
                icon={FolderArchive}
                title={t('archive:browser.empty')}
                className="h-full"
              />
            ) : (
              <BrowserContextMenu
                ctx={actionCtx}
                context="archive"
                viewMenu={null}
                sortMenu={null}
                columnsMenu={null}
                templates={[]}
              >
                <EntryTable
                  items={items}
                  selected={selected}
                  active={active}
                  sort={sort}
                  onSort={setSort}
                  onSelect={(next, act) => {
                    setSelected(next)
                    setActive(act)
                    const one = act ? byPath.get(act) : undefined
                    if (one && !one.isDir && next.size === 1 && !needPassword(one)) setPreview(one)
                  }}
                  onActivate={activate}
                  onUp={() => {
                    if (inner) navigateInner(innerParent(inner))
                  }}
                  onDelete={
                    canModify
                      ? (entries) =>
                          setDialog({ kind: 'remove', entries: entries.map((e) => e.path) })
                      : undefined
                  }
                />
              </BrowserContextMenu>
            )}
            {nextCursor ? (
              <div className="border-t border-border p-2">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    void listArchiveEntries({
                      path: archivePath,
                      inner,
                      encoding,
                      limit: PAGE,
                      cursor: nextCursor,
                      ...(password ? { password } : {}),
                    }).then((r) =>
                      setExtra((x) => ({
                        key: pageKey,
                        items: [...(x.key === pageKey ? x.items : []), ...r.items],
                        ...(r.nextCursor ? { next: r.nextCursor } : {}),
                      })),
                    )
                  }
                  data-testid="archive-load-more"
                >
                  {t('archive:browser.loadMore')}
                </Button>
              </div>
            ) : null}
          </div>
          {preview && byPath.has(preview.path) ? (
            <div className="hidden w-80 shrink-0 md:block lg:w-96">
              <EntryPreview
                archive={archivePath}
                entry={preview}
                session={session}
                onPasswordRequired={() => setDialog({ kind: 'password' })}
              />
            </div>
          ) : null}
        </div>
        <div
          className="flex h-7 shrink-0 items-center gap-3 border-t border-border bg-surface px-3 text-xs text-text-muted tabular"
          role="status"
          data-testid="archive-status"
        >
          <span>
            {t('archive:browser.status.items', {
              count: totalInFolder,
              n: formatNumber(totalInFolder, lang),
            })}
          </span>
          {selectedPaths.length > 0 ? (
            <span data-testid="archive-status-selected">
              {t('archive:browser.status.selected', {
                count: selectedPaths.length,
                n: formatNumber(selectedPaths.length, lang),
              })}
              {selectedBytes > 0 ? ` (${formatBytes(selectedBytes, lang)})` : ''}
            </span>
          ) : null}
          {data?.encrypted ? (
            <span data-testid="archive-status-encrypted">
              {password ? t('archive:browser.status.unlocked') : t('archive:browser.status.locked')}
            </span>
          ) : null}
          {data?.encoding && encoding === 'auto' ? (
            <span>{t('archive:browser.status.encoding', { encoding: data.encoding })}</span>
          ) : null}
        </div>
        {dialog?.kind === 'password' || askPassword ? (
          <PasswordPrompt
            archiveName={name}
            error={
              dialog?.kind === 'password'
                ? dialog.error
                : pwError?.code === 'ARCHIVE_PASSWORD_WRONG'
                  ? t('archive:browser.password.wrong')
                  : undefined
            }
            busy={query.isFetching}
            onSubmit={(pw) => {
              setPassword(pw)
              setPwVersion((v) => v + 1)
              setDialog(null)
            }}
            onCancel={() => {
              setDismissed(pwError)
              setDialog(null)
            }}
          />
        ) : null}
        {dialog?.kind === 'extract' ? (
          <ExtractEntriesDialog
            archiveName={name}
            dir={dir}
            count={dialog.entries.length}
            busy={busy}
            onSubmit={(dest, policy) => void runExtract(dialog.entries, dest, policy)}
            onClose={() => setDialog(null)}
          />
        ) : null}
        {dialog?.kind === 'add' ? (
          <AddFilesDialog
            archivePath={archivePath}
            archiveName={name}
            innerDir={inner}
            busy={busy}
            onSubmit={(sources) => void runAdd(sources)}
            onClose={() => setDialog(null)}
          />
        ) : null}
        {dialog?.kind === 'remove' ? (
          <Dialog open onOpenChange={(o) => (!o ? setDialog(null) : undefined)}>
            <DialogContent className="sm:max-w-sm" data-testid="archive-remove-dialog">
              <DialogHeader>
                <DialogTitle>
                  {t('archive:browser.remove.title', { count: dialog.entries.length })}
                </DialogTitle>
                <DialogDescription>
                  {t('archive:browser.remove.description', { name })}
                </DialogDescription>
              </DialogHeader>
              <ul className="max-h-32 overflow-auto text-xs font-mono">
                {dialog.entries.slice(0, 20).map((p) => (
                  <li key={p} className="truncate">
                    {p}
                  </li>
                ))}
                {dialog.entries.length > 20 ? <li>…</li> : null}
              </ul>
              <DialogFooter>
                <Button variant="outline" onClick={() => setDialog(null)}>
                  {t('fileops:cancel')}
                </Button>
                <Button
                  variant="danger"
                  disabled={busy}
                  onClick={() => void runRemove(dialog.entries)}
                  data-testid="archive-remove-confirm"
                >
                  {t('archive:browser.remove.confirm')}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
