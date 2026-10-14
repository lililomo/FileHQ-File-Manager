import {
  ChevronDown,
  ChevronUp,
  FolderOpen,
  Pause,
  Play,
  RotateCw,
  Trash2,
  Upload as UploadIcon,
  X,
} from 'lucide-react'
import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Progress } from '@/components/ui/progress'
import { pathToSplat } from '@/features/browser/path'
import { formatBytes, formatDuration, formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'
import { summarize, useTransfersStore, type Transfer } from '@/stores/transfers'
import { transferErrorMessage, useUploader } from './useUploader'

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** The panel lists at most this many rows; the rest is summarised. */
const MAX_ROWS = 200

function Row({
  tr,
  lang,
  lt,
  onOpenFolder,
  onPickAgain,
}: {
  tr: Transfer
  lang: string
  lt: LooseT
  onOpenFolder: (dir: string) => void
  onPickAgain: () => void
}) {
  const up = useUploader()
  const percent =
    tr.size > 0 ? Math.min(100, (tr.sent / tr.size) * 100) : tr.status === 'done' ? 100 : 0
  const active = tr.status === 'uploading' || tr.status === 'queued'
  const dir = tr.relativePath ? `${tr.dest === '/' ? '' : tr.dest}/${tr.relativePath}` : tr.dest
  let meta: string
  if (tr.status === 'uploading') {
    meta = `${formatBytes(tr.sent, lang)} / ${formatBytes(tr.size, lang)}`
    if (tr.speedBps && tr.speedBps > 0)
      meta += ` · ${lt('upload:panel.speed', { speed: formatBytes(tr.speedBps, lang) })}`
    if (tr.etaSeconds !== null)
      meta += ` · ${lt('upload:panel.eta', { eta: formatDuration(tr.etaSeconds) })}`
    if (tr.resumedFrom)
      meta += ` · ${lt('upload:panel.resumed', { from: formatBytes(tr.resumedFrom, lang) })}`
  } else if (tr.status === 'done') {
    meta =
      tr.result === 'renamed' && tr.finalPath
        ? lt('upload:panel.renamedTo', {
            name: tr.finalPath.slice(tr.finalPath.lastIndexOf('/') + 1),
          })
        : tr.result === 'overwritten'
          ? lt('upload:panel.overwritten')
          : formatBytes(tr.size, lang)
  } else if (tr.status === 'error') {
    meta = transferErrorMessage(tr)
  } else if (tr.status === 'paused' && tr.detached) {
    meta = lt('upload:panel.detached')
  } else {
    meta = lt(`upload:status.${tr.status}`)
  }
  return (
    <li
      className="border-b border-border px-3 py-2 last:border-b-0"
      data-testid="transfer-row"
      data-transfer-id={tr.id}
      data-status={tr.status}
      data-name={tr.name}
      data-resumed={tr.resumedFrom ? 'true' : undefined}
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm text-text" title={tr.name}>
              {tr.name}
            </span>
            <span className="shrink-0 text-xs text-text-muted" data-testid="transfer-status">
              {lt(`upload:status.${tr.status}`)}
            </span>
          </div>
          <div className="truncate text-xs text-text-muted" title={dir}>
            {lt('upload:panel.to', { dir })}
          </div>
          {active || tr.status === 'paused' ? (
            <Progress
              value={percent}
              className="mt-1 h-1.5"
              aria-label={lt('upload:panel.progress', { percent: Math.round(percent) })}
              data-testid="transfer-progress"
            />
          ) : null}
          <div
            className={cn(
              'mt-0.5 truncate text-xs',
              tr.status === 'error' ? 'text-danger' : 'text-text-muted',
            )}
            data-testid="transfer-meta"
          >
            {meta}
          </div>
        </div>
        <div className="flex shrink-0 items-center">
          {tr.status === 'uploading' || tr.status === 'queued' ? (
            <IconButton
              size="icon-sm"
              label={lt('upload:actions.pause')}
              onClick={() => up.pause(tr.id)}
              data-testid="transfer-pause"
            >
              <Pause className="size-4" aria-hidden="true" />
            </IconButton>
          ) : null}
          {tr.status === 'paused' && !tr.detached ? (
            <IconButton
              size="icon-sm"
              label={lt('upload:actions.resume')}
              onClick={() => up.resume(tr.id)}
              data-testid="transfer-resume"
            >
              <Play className="size-4" aria-hidden="true" />
            </IconButton>
          ) : null}
          {(tr.status === 'paused' && tr.detached) || (tr.status === 'error' && tr.detached) ? (
            <Button
              size="xs"
              variant="outline"
              onClick={onPickAgain}
              data-testid="transfer-pick-again"
            >
              {lt('upload:actions.pickAgain')}
            </Button>
          ) : null}
          {tr.status === 'error' && !tr.detached ? (
            <IconButton
              size="icon-sm"
              label={lt('upload:actions.retry')}
              onClick={() => up.retry(tr.id)}
              data-testid="transfer-retry"
            >
              <RotateCw className="size-4" aria-hidden="true" />
            </IconButton>
          ) : null}
          {tr.status === 'done' && tr.finalPath ? (
            <IconButton
              size="icon-sm"
              label={lt('upload:actions.openFolder')}
              onClick={() =>
                onOpenFolder(tr.finalPath!.slice(0, tr.finalPath!.lastIndexOf('/')) || '/')
              }
              data-testid="transfer-open"
            >
              <FolderOpen className="size-4" aria-hidden="true" />
            </IconButton>
          ) : null}
          {active || tr.status === 'paused' || tr.status === 'error' ? (
            <IconButton
              size="icon-sm"
              label={lt('upload:actions.cancel')}
              onClick={() => up.cancel(tr.id)}
              data-testid="transfer-cancel"
            >
              <X className="size-4" aria-hidden="true" />
            </IconButton>
          ) : (
            <IconButton
              size="icon-sm"
              label={lt('upload:actions.remove')}
              onClick={() => useTransfersStore.getState().remove(tr.id)}
              data-testid="transfer-remove"
            >
              <Trash2 className="size-4" aria-hidden="true" />
            </IconButton>
          )}
        </div>
      </div>
    </li>
  )
}

/** Bottom-right transfer panel (SPEC §8.8 step 4): collapsible, survives
 *  route changes (mounted in AppShell), sits above the task panel. */
export function TransferPanel({ onPickAgain }: { onPickAgain: () => void }) {
  const { t, i18n } = useTranslation(['upload', 'common'])
  const lt = t as unknown as LooseT
  const lang = i18n.resolvedLanguage ?? 'id'
  const navigate = useNavigate()
  const up = useUploader()
  const items = useTransfersStore((s) => s.items)
  const order = useTransfersStore((s) => s.order)
  const open = useTransfersStore((s) => s.panelOpen)
  const collapsed = useTransfersStore((s) => s.collapsed)
  const setPanel = useTransfersStore((s) => s.setPanel)
  const setCollapsed = useTransfersStore((s) => s.setCollapsed)
  const clearFinished = useTransfersStore((s) => s.clearFinished)
  const listRef = useRef<HTMLUListElement>(null)
  const openFolder = useCallback(
    (dir: string) => {
      const splat = pathToSplat(dir)
      void navigate({ to: '/files/$', params: { _splat: splat } })
    },
    [navigate],
  )
  // the panel appears when something is queued (after a reload: detached rows)
  useEffect(() => {
    if (!open && order.length > 0) setPanel(true)
  }, [open, order.length, setPanel])
  if (!open || order.length === 0) return null
  const summary = summarize(items)
  const rows = order.slice(-MAX_ROWS)
  const hidden = order.length - rows.length
  const anyActive = summary.active + summary.queued > 0
  const anyPaused = order.some((id) => items[id]?.status === 'paused' && !items[id]?.detached)
  return (
    <section
      aria-label={lt('upload:panel.title')}
      data-testid="transfer-panel"
      data-active={anyActive ? 'true' : 'false'}
      className="flex max-h-[50vh] w-[min(26rem,calc(100vw-1.5rem))] flex-col rounded-lg border border-border bg-surface shadow-lg"
    >
      <header className="flex items-center gap-2 border-b border-border px-3 py-2">
        <UploadIcon className="size-4 text-text-muted" aria-hidden="true" />
        <h2 className="text-sm font-semibold text-text">
          {lt('upload:panel.title')}
          {anyActive ? (
            <span className="ml-1 text-text-muted" data-testid="transfer-active-count">
              ({formatNumber(summary.active + summary.queued, lang)})
            </span>
          ) : null}
        </h2>
        <span className="ml-auto truncate text-xs text-text-muted" data-testid="transfer-summary">
          {anyActive
            ? [
                `${Math.round(summary.percent)}%`,
                summary.speedBps > 0
                  ? lt('upload:panel.speed', { speed: formatBytes(summary.speedBps, lang) })
                  : '',
                summary.etaSeconds !== null
                  ? lt('upload:panel.eta', { eta: formatDuration(summary.etaSeconds) })
                  : '',
              ]
                .filter(Boolean)
                .join(' · ')
            : lt('upload:panel.idle', { n: formatNumber(order.length, lang) })}
        </span>
        <IconButton
          size="icon-sm"
          label={collapsed ? lt('upload:actions.expand') : lt('upload:actions.collapse')}
          onClick={() => setCollapsed(!collapsed)}
          data-testid="transfer-collapse"
        >
          {collapsed ? (
            <ChevronUp className="size-4" aria-hidden="true" />
          ) : (
            <ChevronDown className="size-4" aria-hidden="true" />
          )}
        </IconButton>
        <IconButton
          size="icon-sm"
          label={lt('common:actions.close')}
          onClick={() => setPanel(false)}
          data-testid="transfer-close"
        >
          <X className="size-4" aria-hidden="true" />
        </IconButton>
      </header>
      {!collapsed ? (
        <>
          <div className="flex flex-wrap items-center gap-1 border-b border-border px-2 py-1">
            {anyActive ? (
              <Button
                size="xs"
                variant="ghost"
                onClick={up.pauseAll}
                data-testid="transfer-pause-all"
              >
                <Pause aria-hidden="true" />
                {lt('upload:actions.pauseAll')}
              </Button>
            ) : null}
            {anyPaused ? (
              <Button
                size="xs"
                variant="ghost"
                onClick={up.resumeAll}
                data-testid="transfer-resume-all"
              >
                <Play aria-hidden="true" />
                {lt('upload:actions.resumeAll')}
              </Button>
            ) : null}
            {summary.detached > 0 ? (
              <Button
                size="xs"
                variant="outline"
                onClick={onPickAgain}
                data-testid="transfer-pick-again-all"
              >
                {lt('upload:actions.pickAgainAll', { n: formatNumber(summary.detached, lang) })}
              </Button>
            ) : null}
            {anyActive || anyPaused ? (
              <Button
                size="xs"
                variant="ghost"
                onClick={up.cancelAll}
                data-testid="transfer-cancel-all"
              >
                <X aria-hidden="true" />
                {lt('upload:actions.cancelAll')}
              </Button>
            ) : null}
            <Button
              size="xs"
              variant="ghost"
              className="ml-auto"
              onClick={clearFinished}
              data-testid="transfer-clear"
            >
              <Trash2 aria-hidden="true" />
              {lt('upload:actions.clearFinished')}
            </Button>
          </div>
          <ul ref={listRef} className="min-h-0 flex-1 overflow-auto" data-testid="transfer-list">
            {hidden > 0 ? (
              <li className="px-3 py-1 text-xs text-text-muted">
                {lt('upload:panel.hidden', { n: formatNumber(hidden, lang) })}
              </li>
            ) : null}
            {rows.map((id) => {
              const tr = items[id]
              return tr ? (
                <Row
                  key={id}
                  tr={tr}
                  lang={lang}
                  lt={lt}
                  onOpenFolder={openFolder}
                  onPickAgain={onPickAgain}
                />
              ) : null
            })}
          </ul>
        </>
      ) : null}
    </section>
  )
}
