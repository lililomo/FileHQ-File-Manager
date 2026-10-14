import { Check, Copy, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type { Item } from '@/features/browser/api'
import { ItemFacts, ItemHeader } from '@/features/browser/ItemDetails'
import { useItemStat } from '@/features/browser/useItemStat'
import {
  CHECKSUM_ALGOS,
  checksum as checksumApi,
  dirSize as dirSizeApi,
  isFileTooLarge,
  verifyChecksum,
  type ChecksumAlgo,
  type DirSizeResult,
} from '@/features/file-ops/api'
import { useFileOpsDialogs } from '@/features/file-ops/store'
import { errorMessage } from '@/features/file-ops/useFileOps'
import { formatBytes, formatNumber } from '@/lib/format'

export type PropertiesTab = 'general' | 'checksum' | 'versions'

export interface PropertiesTarget {
  items: Item[]
  tab?: PropertiesTab
}

const ALGO_LABEL: Record<ChecksumAlgo, string> = { md5: 'MD5', sha1: 'SHA-1', sha256: 'SHA-256' }

function CopyButton({ value, label }: { value: string; label: string }) {
  const { t } = useTranslation('files')
  const [done, setDone] = useState(false)
  return (
    <IconButton
      label={done ? t('props.copied') : label}
      size="sm"
      onClick={() => {
        void navigator.clipboard?.writeText(value).then(() => {
          setDone(true)
          window.setTimeout(() => setDone(false), 1500)
        })
      }}
    >
      {done ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
    </IconButton>
  )
}

/** Properties dialog (Alt+Enter, SPEC §8.7): general facts with folder size,
 *  permission editor, checksum tab with verification and a version-history
 *  placeholder; several items show a summary. */
export function PropertiesDialog({
  target,
  onClose,
  onDirSize,
}: {
  target: PropertiesTarget | null
  onClose: () => void
  /** Called with a computed folder size so the browser can patch its list. */
  onDirSize?: (path: string, res: DirSizeResult) => void
}) {
  const { t } = useTranslation('files')
  const items = target?.items ?? []
  const single = items.length === 1 ? items[0]! : null
  return (
    <Dialog open={!!target} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg" data-testid="properties">
        <DialogHeader>
          <DialogTitle>{t('props.title')}</DialogTitle>
          <DialogDescription>
            {single ? single.path : t('props.multiTitle', { n: items.length })}
          </DialogDescription>
        </DialogHeader>
        {single ? (
          <SingleProperties
            key={single.path}
            item={single}
            initialTab={target?.tab ?? 'general'}
            onClose={onClose}
            onDirSize={onDirSize}
          />
        ) : items.length > 1 ? (
          <MultiProperties items={items} />
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('quickLook.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function SingleProperties({
  item,
  initialTab,
  onClose,
  onDirSize,
}: {
  item: Item
  initialTab: PropertiesTab
  onClose: () => void
  onDirSize?: ((path: string, res: DirSizeResult) => void) | undefined
}) {
  const { t, i18n } = useTranslation(['files', 'fileops', 'errors'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const stat = useItemStat(item.path)
  const warnings = stat.data?.warnings ?? []
  const [tab, setTab] = useState<PropertiesTab>(initialTab)
  const [size, setSize] = useState<DirSizeResult | null>(null)
  const [sizing, setSizing] = useState(false)
  const openDialog = useFileOpsDialogs((s) => s.open)
  const shownSize = size ?? stat.data?.dirSize ?? null

  const compute = async () => {
    setSizing(true)
    try {
      const res = await dirSizeApi(item.path)
      setSize(res)
      onDirSize?.(item.path, res)
    } catch (e) {
      toast.error(
        errorMessage(t as unknown as (k: string, o?: Record<string, unknown>) => string, e),
      )
    } finally {
      setSizing(false)
    }
  }
  return (
    <Tabs value={tab} onValueChange={(v) => setTab(v as PropertiesTab)}>
      <TabsList className="w-full">
        <TabsTrigger value="general" data-testid="props-tab-general">
          {t('files:props.tabGeneral')}
        </TabsTrigger>
        {!item.isDir ? (
          <TabsTrigger value="checksum" data-testid="props-tab-checksum">
            {t('files:props.tabChecksum')}
          </TabsTrigger>
        ) : null}
        <TabsTrigger value="versions" data-testid="props-tab-versions">
          {t('files:props.tabVersions')}
        </TabsTrigger>
      </TabsList>
      <TabsContent value="general" className="space-y-4 pt-3">
        <ItemHeader item={item} size={48} />
        <ItemFacts item={item} stat={stat.data?.stat} versionsCount={stat.data?.versionsCount} />
        {item.isDir ? (
          <div className="flex flex-wrap items-center gap-2 text-xs" data-testid="props-dir-size">
            <span className="text-text-muted">{t('files:props.size')}</span>
            {shownSize ? (
              <span className="tabular" data-testid="props-dir-size-value">
                {t(shownSize.partial ? 'files:props.dirSizePartial' : 'files:props.dirSize', {
                  size: formatBytes(shownSize.size, lang),
                  files: formatNumber(shownSize.files, lang),
                  dirs: formatNumber(shownSize.dirs, lang),
                })}
              </span>
            ) : null}
            <Button
              size="sm"
              variant="outline"
              onClick={() => void compute()}
              disabled={sizing}
              data-testid="props-compute-size"
            >
              {sizing
                ? t('files:props.computing')
                : shownSize
                  ? t('files:props.recompute')
                  : t('files:props.compute')}
            </Button>
          </div>
        ) : null}
        <div className="flex items-center gap-2 text-xs">
          <span className="text-text-muted">{t('files:props.mode')}</span>
          <span className="font-mono">
            {item.mode} {item.modeOctal}
          </span>
          {!item.isSymlink ? (
            <Button
              size="sm"
              variant="outline"
              data-testid="props-change-permission"
              onClick={() => {
                onClose()
                openDialog({ kind: 'permissions', items: [item] })
              }}
            >
              {t('files:props.changePermission')}
            </Button>
          ) : null}
        </div>
        {warnings.length > 0 ? (
          <p className="text-xs text-warning" role="note">
            {t('files:props.warning', { codes: warnings.join(', ') })}
          </p>
        ) : null}
      </TabsContent>
      {!item.isDir ? (
        <TabsContent value="checksum" className="pt-3">
          <ChecksumPanel item={item} initial={stat.data?.checksums} />
        </TabsContent>
      ) : null}
      <TabsContent value="versions" className="pt-3">
        <p className="text-sm text-text-muted" data-testid="props-versions-placeholder">
          {t('files:props.versionsSoon')}
        </p>
      </TabsContent>
    </Tabs>
  )
}

function ChecksumPanel({
  item,
  initial,
}: {
  item: Item
  initial?: Record<string, string> | undefined
}) {
  const { t } = useTranslation(['files', 'fileops', 'errors'])
  const lt = t as unknown as (k: string, o?: Record<string, unknown>) => string
  const [sums, setSums] = useState<Partial<Record<ChecksumAlgo, string>>>(() => ({
    ...(initial ?? {}),
  }))
  const [busy, setBusy] = useState<ChecksumAlgo | 'all' | null>(null)
  const [tooLarge, setTooLarge] = useState(false)
  const [algo, setAlgo] = useState<ChecksumAlgo>('sha256')
  const [expected, setExpected] = useState('')
  const [verdict, setVerdict] = useState<{ match: boolean; actual: string } | null>(null)
  const [verifying, setVerifying] = useState(false)

  const run = async (algos: ChecksumAlgo[], key: ChecksumAlgo | 'all') => {
    setBusy(key)
    try {
      const res = await checksumApi(item.path, algos)
      setSums((prev) => {
        const next = { ...prev }
        for (const a of CHECKSUM_ALGOS) if (res[a]) next[a] = res[a]
        return next
      })
    } catch (e) {
      if (isFileTooLarge(e)) setTooLarge(true)
      else toast.error(errorMessage(lt, e))
    } finally {
      setBusy(null)
    }
  }
  const verify = async () => {
    if (!expected.trim()) return
    setVerifying(true)
    try {
      const res = await verifyChecksum(item.path, algo, expected)
      setVerdict({ match: res.match, actual: res.actual })
      setSums((prev) => ({ ...prev, [algo]: res.actual }))
    } catch (e) {
      if (isFileTooLarge(e)) setTooLarge(true)
      else toast.error(errorMessage(lt, e))
    } finally {
      setVerifying(false)
    }
  }
  const missing = CHECKSUM_ALGOS.filter((a) => !sums[a])
  return (
    <div className="space-y-4 text-sm" data-testid="checksum-panel">
      <p className="text-xs text-text-muted">{t('files:props.checksumIntro')}</p>
      {tooLarge ? (
        <p className="text-xs text-warning" role="note" data-testid="checksum-too-large">
          {t('files:props.checksumTooLarge')}
        </p>
      ) : null}
      <dl className="grid grid-cols-[auto_1fr_auto] items-center gap-x-3 gap-y-2 text-xs">
        {CHECKSUM_ALGOS.map((a) => (
          <div key={a} className="contents">
            <dt className="text-text-muted">{ALGO_LABEL[a]}</dt>
            <dd className="min-w-0 font-mono break-all" data-testid={`checksum-${a}`}>
              {sums[a] ?? '—'}
            </dd>
            <dd className="flex justify-end">
              {sums[a] ? (
                <CopyButton value={sums[a]!} label={t('files:props.copyValue')} />
              ) : (
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy !== null || tooLarge}
                  onClick={() => void run([a], a)}
                  data-testid={`checksum-compute-${a}`}
                >
                  {busy === a ? t('files:props.computing') : t('files:props.compute')}
                </Button>
              )}
            </dd>
          </div>
        ))}
      </dl>
      {missing.length > 1 ? (
        <Button
          size="sm"
          variant="outline"
          disabled={busy !== null || tooLarge}
          onClick={() => void run(missing, 'all')}
          data-testid="checksum-compute-all"
        >
          {busy === 'all' ? t('files:props.computing') : t('files:props.checksumAll')}
        </Button>
      ) : null}
      <form
        className="space-y-2 rounded-md border border-border p-3"
        onSubmit={(e) => {
          e.preventDefault()
          void verify()
        }}
      >
        <Label htmlFor="checksum-expected">{t('files:props.verifyTitle')}</Label>
        <div className="flex flex-wrap gap-2">
          <Select value={algo} onValueChange={(v) => setAlgo(v as ChecksumAlgo)}>
            <SelectTrigger
              className="w-28"
              aria-label={t('files:props.verifyTitle')}
              data-testid="checksum-verify-algo"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {CHECKSUM_ALGOS.map((a) => (
                <SelectItem key={a} value={a}>
                  {ALGO_LABEL[a]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Input
            id="checksum-expected"
            className="min-w-0 flex-1 font-mono"
            value={expected}
            onChange={(e) => {
              setExpected(e.target.value)
              setVerdict(null)
            }}
            placeholder={t('files:props.verifyPlaceholder')}
            autoComplete="off"
            spellCheck={false}
            data-testid="checksum-verify-input"
          />
          <Button
            type="submit"
            size="sm"
            disabled={verifying || !expected.trim() || tooLarge}
            data-testid="checksum-verify"
          >
            {t('files:props.verify')}
          </Button>
        </div>
        {verdict ? (
          <p
            className={
              verdict.match
                ? 'flex items-center gap-1 text-xs text-success'
                : 'flex items-center gap-1 text-xs text-danger'
            }
            role="status"
            data-testid="checksum-verify-result"
            data-match={verdict.match}
          >
            {verdict.match ? (
              <Check className="size-3.5" aria-hidden="true" />
            ) : (
              <X className="size-3.5" aria-hidden="true" />
            )}
            {verdict.match
              ? t('files:props.verifyMatch')
              : t('files:props.verifyMismatch', { actual: verdict.actual })}
          </p>
        ) : null}
      </form>
    </div>
  )
}

function MultiProperties({ items }: { items: Item[] }) {
  const { t, i18n } = useTranslation('files')
  const lang = i18n.resolvedLanguage ?? 'id'
  const files = items.filter((it) => !it.isDir)
  const folders = items.length - files.length
  const bytes = files.reduce((n, it) => n + it.size, 0)
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs" data-testid="props-multi">
      <dt className="text-text-muted">
        {t('props.multiTitle', { n: formatNumber(items.length, lang) })}
      </dt>
      <dd>
        {t('props.multiFiles', { count: files.length, n: formatNumber(files.length, lang) })}
        {folders > 0
          ? `, ${t('props.multiFolders', { count: folders, n: formatNumber(folders, lang) })}`
          : ''}
      </dd>
      <dt className="text-text-muted">{t('props.multiSize')}</dt>
      <dd className="tabular" data-testid="props-multi-size">
        {formatBytes(bytes, lang)}
      </dd>
      {folders > 0 ? (
        <dd className="col-span-2 text-text-muted" role="note">
          {t('props.multiNote')}
        </dd>
      ) : null}
      <dt className="text-text-muted">{t('props.location')}</dt>
      <dd className="font-mono break-all">
        {items[0]!.path.split('/').slice(0, -1).join('/') || '/'}
      </dd>
    </dl>
  )
}
