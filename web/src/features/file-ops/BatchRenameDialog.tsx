import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { baseName } from '@/features/browser/path'
import { ApiError } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'
import { batchRenamePreview, type PreviewItem, type RenameRule } from './api'
import { DEFAULT_FORM, toRule, type RuleForm } from './batchRule'
import { errorMessage } from './useFileOps'

export interface BatchRenameDialogProps {
  paths: string[]
  onClose: () => void
  /** Applies the rule; resolves when done (errors are shown by the caller). */
  onApply: (paths: string[], rule: RenameRule) => Promise<unknown>
  onCloseAutoFocus?: (e: Event) => void
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

/** Batch rename (PROMPT 9b task 8): rule builder, live preview from the API,
 *  collision/invalid rows flagged, "Terapkan" disabled until the preview is clean. */
export function BatchRenameDialog({
  paths,
  onClose,
  onApply,
  onCloseAutoFocus,
}: BatchRenameDialogProps) {
  const { t, i18n } = useTranslation(['fileops', 'errors'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const [form, setForm] = useState<RuleForm>(DEFAULT_FORM)
  const [applying, setApplying] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [serverItems, setServerItems] = useState<PreviewItem[] | null>(null)
  const rule = toRule(useDebounced(form, 250))
  const ruleKey = JSON.stringify(rule)
  const preview = useQuery({
    queryKey: ['batch-rename-preview', paths, ruleKey],
    queryFn: () => batchRenamePreview(paths, rule),
    placeholderData: keepPreviousData,
    retry: false,
    staleTime: 0,
  })
  const items =
    serverItems ??
    preview.data?.items ??
    paths.map((p) => ({ from: p, to: baseName(p), status: 'ok' as const }))
  const clean = !serverItems && preview.data?.clean === true
  // the server answers `to` as a full path for valid rows and a bare name for invalid ones
  const changed = items.filter((it) => baseName(it.from) !== baseName(it.to)).length
  const canApply = clean && changed > 0 && !applying && !preview.isFetching
  const set = <K extends keyof RuleForm>(k: K, v: RuleForm[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setServerItems(null)
    setError(null)
  }

  const apply = async () => {
    setApplying(true)
    setError(null)
    try {
      await onApply(paths, rule)
      onClose()
    } catch (e) {
      if (e instanceof ApiError && e.code === 'CONFLICT' && Array.isArray(e.details.items)) {
        setServerItems(e.details.items as PreviewItem[])
      }
      setError(errorMessage(t as unknown as (k: string, o?: Record<string, unknown>) => string, e))
    } finally {
      setApplying(false)
    }
  }

  const statusLabel = (it: PreviewItem) => {
    if (it.status === 'ok')
      return baseName(it.from) === baseName(it.to)
        ? t('fileops:batch.unchanged')
        : t('fileops:batch.ok')
    const reason = it.reason
      ? t(`fileops:batch.reason.${it.reason}`, { defaultValue: it.reason })
      : ''
    return `${t(`fileops:batch.${it.status}`)}${reason ? ` · ${reason}` : ''}`
  }

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent
        className="max-h-[90vh] overflow-auto sm:max-w-2xl"
        data-testid="batch-rename-dialog"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <DialogHeader>
          <DialogTitle>{t('fileops:batch.title')}</DialogTitle>
          <DialogDescription>
            {t('fileops:batch.description', {
              count: paths.length,
              n: formatNumber(paths.length, lang),
            })}
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-3 sm:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (canApply) void apply()
          }}
        >
          <div className="space-y-1">
            <Label htmlFor="br-find">{t('fileops:batch.find')}</Label>
            <Input
              id="br-find"
              value={form.find}
              onChange={(e) => set('find', e.target.value)}
              autoFocus
              spellCheck={false}
              data-testid="br-find"
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="br-replace">{t('fileops:batch.replace')}</Label>
            <Input
              id="br-replace"
              value={form.replace}
              onChange={(e) => set('replace', e.target.value)}
              spellCheck={false}
              data-testid="br-replace"
            />
          </div>
          <div className="flex flex-wrap items-center gap-4 sm:col-span-2">
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={form.regex}
                onCheckedChange={(v) => set('regex', v === true)}
                data-testid="br-regex"
              />
              {t('fileops:batch.regex')}
            </label>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={form.caseInsensitive}
                onCheckedChange={(v) => set('caseInsensitive', v === true)}
                data-testid="br-ci"
              />
              {t('fileops:batch.caseInsensitive')}
            </label>
          </div>
          <div className="space-y-1">
            <Label htmlFor="br-case">{t('fileops:batch.case')}</Label>
            <Select
              value={form.caseMode}
              onValueChange={(v) => set('caseMode', v as RuleForm['caseMode'])}
            >
              <SelectTrigger id="br-case" data-testid="br-case">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">{t('fileops:batch.caseNone')}</SelectItem>
                <SelectItem value="upper">{t('fileops:batch.caseUpper')}</SelectItem>
                <SelectItem value="lower">{t('fileops:batch.caseLower')}</SelectItem>
                <SelectItem value="title">{t('fileops:batch.caseTitle')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <Label htmlFor="br-ext">{t('fileops:batch.extMode')}</Label>
            <Select
              value={form.extMode}
              onValueChange={(v) => set('extMode', v as RuleForm['extMode'])}
            >
              <SelectTrigger id="br-ext" data-testid="br-extmode">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="keep">{t('fileops:batch.extKeep')}</SelectItem>
                <SelectItem value="include">{t('fileops:batch.extInclude')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="sm:col-span-2">
            <label className="flex items-center gap-2 text-sm">
              <Switch
                checked={form.numbering}
                onCheckedChange={(v) => set('numbering', v)}
                data-testid="br-numbering"
              />
              {t('fileops:batch.numbering')}
            </label>
          </div>
          {form.numbering ? (
            <div className="grid grid-cols-[2fr_1fr_1fr_1fr] gap-2 sm:col-span-2">
              <div className="space-y-1">
                <Label htmlFor="br-pattern">{t('fileops:batch.pattern')}</Label>
                <Input
                  id="br-pattern"
                  value={form.pattern}
                  onChange={(e) => set('pattern', e.target.value)}
                  spellCheck={false}
                  data-testid="br-pattern"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="br-start">{t('fileops:batch.start')}</Label>
                <Input
                  id="br-start"
                  type="number"
                  value={form.start}
                  onChange={(e) => set('start', e.target.value)}
                  data-testid="br-start"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="br-step">{t('fileops:batch.step')}</Label>
                <Input
                  id="br-step"
                  type="number"
                  value={form.step}
                  onChange={(e) => set('step', e.target.value)}
                  data-testid="br-step"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="br-pad">{t('fileops:batch.pad')}</Label>
                <Input
                  id="br-pad"
                  type="number"
                  min={0}
                  max={12}
                  value={form.pad}
                  onChange={(e) => set('pad', e.target.value)}
                  data-testid="br-pad"
                />
              </div>
            </div>
          ) : null}
          <div className="sm:col-span-2">
            <div className="mb-1 flex items-center justify-between text-xs text-text-muted">
              <span>{t('fileops:batch.preview')}</span>
              <span aria-live="polite" data-testid="br-summary">
                {preview.isFetching
                  ? t('fileops:batch.loading')
                  : t('fileops:batch.summary', {
                      changed: formatNumber(changed, lang),
                      n: formatNumber(items.length, lang),
                    })}
              </span>
            </div>
            <div className="max-h-56 overflow-auto rounded-md border border-border">
              <table className="w-full text-xs">
                <thead className="sticky top-0 bg-surface-2 text-left text-text-muted">
                  <tr>
                    <th className="px-2 py-1 font-medium">{t('fileops:batch.from')}</th>
                    <th className="px-2 py-1 font-medium">{t('fileops:batch.to')}</th>
                    <th className="px-2 py-1 font-medium">{t('fileops:batch.status')}</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((it, i) => (
                    <tr
                      key={it.from}
                      className={cn(
                        'border-t border-border/60',
                        it.status !== 'ok' && 'bg-danger/5',
                      )}
                      data-testid={`br-row-${i}`}
                      data-status={it.status}
                    >
                      <td className="truncate px-2 py-1 font-mono">{baseName(it.from)}</td>
                      <td className="truncate px-2 py-1 font-mono" data-testid={`br-to-${i}`}>
                        {baseName(it.to)}
                      </td>
                      <td className="px-2 py-1">
                        <Badge variant={it.status === 'ok' ? 'secondary' : 'destructive'}>
                          {statusLabel(it)}
                        </Badge>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
          {preview.isError ? (
            <p className="text-xs text-danger sm:col-span-2" role="alert">
              {errorMessage(
                t as unknown as (k: string, o?: Record<string, unknown>) => string,
                preview.error,
              )}
            </p>
          ) : null}
          {error ? (
            <p className="text-xs text-danger sm:col-span-2" role="alert">
              {error}
            </p>
          ) : null}
          <DialogFooter className="sm:col-span-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t('fileops:cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!canApply} data-testid="br-apply">
              {t('fileops:batch.apply')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
