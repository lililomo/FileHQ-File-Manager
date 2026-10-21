import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Link2, QrCode as QrIcon } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
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
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { Textarea } from '@/components/ui/textarea'
import type { Item } from '@/features/browser/api'
import { errorMessage } from '@/lib/errors'
import { useSessionStore } from '@/stores/session'
import {
  createShare,
  deleteShare,
  disableShare,
  enableShare,
  regenerateShareToken,
  sharesByPath,
  sharesByPathKey,
  type Share,
  type ShareInput,
} from './api'
import { QrCode } from './QrCode'

export interface ShareDialogProps {
  item: Pick<Item, 'path' | 'name' | 'isDir'>
  onClose: () => void
  onCloseAutoFocus?: (e: Event) => void
}

type Expiry = '1' | '7' | '30' | 'custom' | 'never'

/** Copies text; false when the clipboard is unavailable. */
async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

function fmtDate(iso: string, lang: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return new Intl.DateTimeFormat(lang, { dateStyle: 'medium', timeStyle: 'short' }).format(d)
}

/** "Share" dialog (SPEC §8.15, PROMPT 18a task 7): existing public links of
 *  the item with copy/QR/disable/delete, and a form for a new link
 *  (password, expiry, download limit, preview-only with its honest note,
 *  file request, hide owner, custom slug, note). Share to users = Step 18b. */
export function ShareDialog({ item, onClose, onCloseAutoFocus }: ShareDialogProps) {
  const { t, i18n } = useTranslation(['share', 'errors'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const config = useSessionStore((s) => s.config)
  const publicEnabled = config?.publicSharesEnabled !== false
  const requirePassword = config?.shareRequirePassword === true
  const allowNoExpiry = config?.shareAllowNoExpiry !== false
  const existing = useQuery({
    queryKey: sharesByPathKey(item.path),
    queryFn: ({ signal }) => sharesByPath(item.path, signal),
    staleTime: 5_000,
  })
  const [created, setCreated] = useState<Share | null>(null)

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: sharesByPathKey(item.path) })
    void qc.invalidateQueries({ queryKey: ['list'] })
    void qc.invalidateQueries({ queryKey: ['stat', item.path] })
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="max-h-[90dvh] overflow-y-auto sm:max-w-xl"
        onCloseAutoFocus={onCloseAutoFocus}
        data-testid="share-dialog"
      >
        <DialogHeader>
          <DialogTitle className="truncate">
            {t('share:dialog.title', { name: item.name })}
          </DialogTitle>
          <DialogDescription>{item.path}</DialogDescription>
        </DialogHeader>
        {created ? (
          <ShareResult share={created} lang={lang} onDone={() => setCreated(null)} />
        ) : (
          <>
            <section aria-label={t('share:dialog.existing')} className="space-y-2">
              <h3 className="text-xs font-semibold tracking-wide text-text-muted uppercase">
                {t('share:dialog.existing')}
              </h3>
              {existing.data && existing.data.items.length === 0 ? (
                <p className="text-sm text-text-muted" data-testid="share-none">
                  {t('share:dialog.none')}
                </p>
              ) : null}
              {existing.data?.items.map((s) => (
                <ShareRow key={s.id} share={s} lang={lang} onChanged={invalidate} />
              ))}
            </section>
            {publicEnabled ? (
              <ShareForm
                item={item}
                requirePassword={requirePassword}
                allowNoExpiry={allowNoExpiry}
                onCreated={(s) => {
                  setCreated(s)
                  invalidate()
                }}
              />
            ) : (
              <p className="text-sm text-text-muted">{t('share:dialog.disabledByAdmin')}</p>
            )}
          </>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('share:dialog.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ShareForm({
  item,
  requirePassword,
  allowNoExpiry,
  onCreated,
}: {
  item: Pick<Item, 'path' | 'isDir'>
  requirePassword: boolean
  allowNoExpiry: boolean
  onCreated: (s: Share) => void
}) {
  const { t } = useTranslation(['share', 'errors'])
  const [password, setPassword] = useState('')
  const [expiry, setExpiry] = useState<Expiry>('7')
  const [expiryDate, setExpiryDate] = useState('')
  const [maxDownloads, setMaxDownloads] = useState('')
  const [allowDownload, setAllowDownload] = useState(true)
  const [allowUpload, setAllowUpload] = useState(false)
  const [hideListing, setHideListing] = useState(false)
  const [hideOwner, setHideOwner] = useState(false)
  const [slug, setSlug] = useState('')
  const [note, setNote] = useState('')
  const [error, setError] = useState<string | null>(null)
  const create = useMutation({
    mutationFn: (input: ShareInput) => createShare(input),
    onSuccess: (res) => {
      toast.success(t('share:toast.created'))
      onCreated(res.share)
    },
    onError: (e) => setError(errorMessage(e)),
  })
  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    const input: ShareInput = {
      path: item.path,
      mode: 'public',
      allowDownload,
      hideOwner,
      ...(password ? { password } : {}),
      ...(slug.trim() ? { slug: slug.trim().toLowerCase() } : {}),
      ...(note.trim() ? { note: note.trim() } : {}),
      ...(maxDownloads ? { maxDownloads: Number(maxDownloads) } : {}),
      ...(item.isDir ? { allowUpload, hideListing: allowUpload && hideListing } : {}),
    }
    if (expiry === 'never') input.expiresInDays = 0
    else if (expiry === 'custom') input.expiresAt = new Date(expiryDate).toISOString()
    else input.expiresInDays = Number(expiry)
    create.mutate(input)
  }
  return (
    <form
      onSubmit={submit}
      className="space-y-3 border-t border-border pt-3"
      data-testid="share-form"
    >
      <h3 className="text-xs font-semibold tracking-wide text-text-muted uppercase">
        {t('share:dialog.newLink')}
      </h3>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="share-password">{t('share:field.password')}</Label>
          <Input
            id="share-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required={requirePassword}
            minLength={4}
            data-testid="share-password"
          />
          <p className="text-xs text-text-muted">
            {requirePassword ? t('share:field.passwordRequired') : t('share:field.passwordHint')}
          </p>
        </div>
        <div className="space-y-1">
          <Label htmlFor="share-expiry">{t('share:field.expiry')}</Label>
          <Select value={expiry} onValueChange={(v) => setExpiry(v as Expiry)}>
            <SelectTrigger id="share-expiry" data-testid="share-expiry">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="1">{t('share:field.expiry1')}</SelectItem>
              <SelectItem value="7">{t('share:field.expiry7')}</SelectItem>
              <SelectItem value="30">{t('share:field.expiry30')}</SelectItem>
              <SelectItem value="custom">{t('share:field.expiryCustom')}</SelectItem>
              {allowNoExpiry ? (
                <SelectItem value="never">{t('share:field.expiryNever')}</SelectItem>
              ) : null}
            </SelectContent>
          </Select>
          {expiry === 'custom' ? (
            <Input
              type="datetime-local"
              aria-label={t('share:field.expiryDate')}
              value={expiryDate}
              onChange={(e) => setExpiryDate(e.target.value)}
              required
            />
          ) : null}
        </div>
        <div className="space-y-1">
          <Label htmlFor="share-max">{t('share:field.maxDownloads')}</Label>
          <Input
            id="share-max"
            type="number"
            min={1}
            inputMode="numeric"
            value={maxDownloads}
            onChange={(e) => setMaxDownloads(e.target.value)}
            data-testid="share-max-downloads"
          />
          <p className="text-xs text-text-muted">{t('share:field.maxDownloadsHint')}</p>
        </div>
        <div className="space-y-1">
          <Label htmlFor="share-slug">{t('share:field.slug')}</Label>
          <div className="flex items-center gap-1">
            <span className="text-sm text-text-muted">{t('share:field.slugPrefix')}</span>
            <Input
              id="share-slug"
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
              pattern="[a-z0-9][a-z0-9-]{2,63}"
              data-testid="share-slug"
            />
          </div>
          <p className="text-xs text-text-muted">{t('share:field.slugHint')}</p>
        </div>
      </div>
      <div className="space-y-2">
        <label className="flex items-center gap-2 text-sm">
          <Switch
            checked={allowDownload}
            onCheckedChange={setAllowDownload}
            data-testid="share-allow-download"
          />
          {t('share:field.allowDownload')}
        </label>
        {!allowDownload ? (
          <p
            className="rounded border border-border bg-surface-2 px-2 py-1 text-xs"
            data-testid="share-preview-note"
          >
            {t('share:field.previewOnlyNote')}
          </p>
        ) : null}
        {item.isDir ? (
          <>
            <label className="flex items-center gap-2 text-sm">
              <Switch
                checked={allowUpload}
                onCheckedChange={setAllowUpload}
                data-testid="share-allow-upload"
              />
              {t('share:field.allowUpload')}
            </label>
            {allowUpload ? (
              <label className="flex items-center gap-2 pl-6 text-sm">
                <Switch checked={hideListing} onCheckedChange={setHideListing} />
                {t('share:field.hideListing')}
              </label>
            ) : null}
          </>
        ) : null}
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={hideOwner} onCheckedChange={setHideOwner} />
          {t('share:field.hideOwner')}
        </label>
      </div>
      <div className="space-y-1">
        <Label htmlFor="share-note">{t('share:field.note')}</Label>
        <Textarea
          id="share-note"
          rows={2}
          maxLength={500}
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      </div>
      {error ? (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      ) : null}
      <Button type="submit" disabled={create.isPending} data-testid="share-create">
        <Link2 aria-hidden="true" />
        {create.isPending ? t('share:dialog.creating') : t('share:dialog.create')}
      </Button>
    </form>
  )
}

/** The freshly created link: copy, QR and a summary of its protection. */
function ShareResult({ share, lang, onDone }: { share: Share; lang: string; onDone: () => void }) {
  const { t } = useTranslation('share')
  const [qr, setQr] = useState(false)
  const [copied, setCopied] = useState(false)
  return (
    <div className="space-y-3" data-testid="share-result">
      <h3 className="text-sm font-semibold">{t('result.title')}</h3>
      <div className="flex items-center gap-2">
        <Input
          readOnly
          value={share.url}
          onFocus={(e) => e.currentTarget.select()}
          data-testid="share-link"
        />
        <Button
          variant="outline"
          onClick={async () => {
            const ok = await copyText(share.url)
            if (ok) {
              setCopied(true)
              toast.success(t('result.copied'))
            } else toast.error(t('toast.copyFailed'))
          }}
          data-testid="share-copy"
        >
          {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
          {t('result.copy')}
        </Button>
        <Button
          variant="outline"
          onClick={() => setQr((v) => !v)}
          aria-pressed={qr}
          data-testid="share-qr-toggle"
        >
          <QrIcon aria-hidden="true" />
          {qr ? t('result.hideQr') : t('result.showQr')}
        </Button>
      </div>
      {qr ? (
        <div className="flex justify-center">
          <QrCode value={share.url} />
        </div>
      ) : null}
      <ul className="flex flex-wrap gap-1 text-xs">
        {share.hasPassword ? <Badge variant="secondary">{t('result.password')}</Badge> : null}
        <Badge variant="secondary">
          {share.expiresAt
            ? t('result.expires', { date: fmtDate(share.expiresAt, lang) })
            : t('result.noExpiry')}
        </Badge>
        {share.maxDownloads ? (
          <Badge variant="secondary">{t('result.limit', { n: share.maxDownloads })}</Badge>
        ) : null}
        {!share.allowDownload ? <Badge variant="secondary">{t('result.previewOnly')}</Badge> : null}
        {share.allowUpload ? <Badge variant="secondary">{t('result.upload')}</Badge> : null}
      </ul>
      <Button variant="ghost" size="sm" onClick={onDone}>
        {t('dialog.existing')}
      </Button>
    </div>
  )
}

/** One existing link with its status, counters and actions. */
function ShareRow({
  share,
  lang,
  onChanged,
}: {
  share: Share
  lang: string
  onChanged: () => void
}) {
  const { t } = useTranslation('share')
  const [confirm, setConfirm] = useState<'delete' | 'regenerate' | null>(null)
  const [qr, setQr] = useState(false)
  const run = useMutation({
    mutationFn: async (action: 'disable' | 'enable' | 'delete' | 'regenerate') => {
      switch (action) {
        case 'disable':
          await disableShare(share.id)
          return t('toast.disabled')
        case 'enable':
          await enableShare(share.id)
          return t('toast.enabled')
        case 'delete':
          await deleteShare(share.id)
          return t('toast.deleted')
        case 'regenerate':
          await regenerateShareToken(share.id)
          return t('toast.regenerated')
      }
    },
    onSuccess: (msg) => {
      toast.success(msg)
      onChanged()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const reason = share.disabledReason ? t(`row.reason.${share.disabledReason}`) : null
  const ownerCanEnable =
    share.disabledReason !== 'admin_revoked' && share.disabledReason !== 'owner_disabled'
  return (
    <div
      className="rounded border border-border p-2 text-sm"
      data-testid="share-row"
      data-status={share.status}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={share.status === 'active' ? 'default' : 'secondary'}>
          {t(`row.status.${share.status}`)}
          {reason ? ` · ${reason}` : ''}
        </Badge>
        <span className="text-xs text-text-muted">
          {t('row.views', { n: share.viewCount })} ·{' '}
          {t('row.downloads', { n: share.downloadCount })}
          {share.expiresAt
            ? ` · ${t('result.expires', { date: fmtDate(share.expiresAt, lang) })}`
            : ''}
        </span>
      </div>
      <div className="mt-1 flex items-center gap-1">
        {share.url ? (
          <Input
            readOnly
            value={share.url}
            className="h-8 text-xs"
            onFocus={(e) => e.currentTarget.select()}
          />
        ) : (
          <span className="text-xs text-text-muted">{t('row.linkUnavailable')}</span>
        )}
        {share.url ? (
          <Button
            variant="ghost"
            size="icon"
            aria-label={t('result.copy')}
            onClick={async () => {
              if (await copyText(share.url)) toast.success(t('result.copied'))
              else toast.error(t('toast.copyFailed'))
            }}
            data-testid="share-row-copy"
          >
            <Copy aria-hidden="true" />
          </Button>
        ) : null}
        {share.url ? (
          <Button
            variant="ghost"
            size="icon"
            aria-label={t('result.showQr')}
            aria-pressed={qr}
            onClick={() => setQr((v) => !v)}
          >
            <QrIcon aria-hidden="true" />
          </Button>
        ) : null}
      </div>
      {qr && share.url ? (
        <div className="mt-2 flex justify-center">
          <QrCode value={share.url} size={160} />
        </div>
      ) : null}
      <div className="mt-2 flex flex-wrap gap-1">
        {share.status === 'disabled' ? (
          ownerCanEnable ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => run.mutate('enable')}
              data-testid="share-enable"
            >
              {t('row.enable')}
            </Button>
          ) : null
        ) : (
          <Button
            variant="outline"
            size="sm"
            onClick={() => run.mutate('disable')}
            data-testid="share-disable"
          >
            {t('row.disable')}
          </Button>
        )}
        <Button variant="outline" size="sm" onClick={() => setConfirm('regenerate')}>
          {t('row.regenerate')}
        </Button>
        <Button
          variant="destructive"
          size="sm"
          onClick={() => setConfirm('delete')}
          data-testid="share-delete"
        >
          {t('row.delete')}
        </Button>
      </div>
      <AlertDialog open={confirm !== null} onOpenChange={(open) => !open && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirm === 'delete' ? t('confirm.deleteTitle') : t('confirm.regenerateTitle')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirm === 'delete' ? t('confirm.deleteBody') : t('confirm.regenerateBody')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('dialog.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                const action = confirm === 'delete' ? 'delete' : 'regenerate'
                setConfirm(null)
                run.mutate(action)
              }}
              data-testid="share-confirm"
            >
              {confirm === 'delete' ? t('row.delete') : t('row.regenerate')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
