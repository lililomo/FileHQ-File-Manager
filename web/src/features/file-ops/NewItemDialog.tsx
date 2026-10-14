import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { createFile, mkdir, type Item } from '@/features/browser/api'
import { baseName, joinPath } from '@/features/browser/path'
import { announce } from '@/lib/announce'
import { listTemplates } from './api'
import { FolderPicker } from './FolderPicker'
import { validateName } from './nameValidation'
import type { NewItemMode } from './store'
import { errorMessage, relativeTarget } from './useFileOps'

export type NewItemKind = NewItemMode

export interface NewItemDialogProps {
  mode: NewItemMode
  dir: string
  /** symlink: preselected target (absolute virtual path) */
  target?: string
  /** template: preselected template name */
  template?: string
  onClose: () => void
  onCreated?: (item: Item) => void
  /** symlink: performs the creation (the ops hook owns toasts/undo). */
  onSymlink?: (linkPath: string, target: string) => Promise<unknown>
  onCloseAutoFocus?: (e: Event) => void
}

type LooseT = (key: string, options?: Record<string, unknown>) => string

/** "Baru" dialog (PROMPT 9b task 9): empty file, folder, file from a
 *  template (`GET /files/templates`) or a symlink with a target picker. */
export function NewItemDialog({
  mode,
  dir,
  target,
  template: initialTemplate,
  onClose,
  onCreated,
  onSymlink,
  onCloseAutoFocus,
}: NewItemDialogProps) {
  const { t } = useTranslation(['files', 'fileops', 'common', 'errors'])
  const lt = t as unknown as LooseT
  const qc = useQueryClient()
  const templates = useQuery({
    queryKey: ['templates'],
    queryFn: ({ signal }) => listTemplates(signal),
    enabled: mode === 'template',
    staleTime: Infinity,
  })
  const [template, setTemplate] = useState<string>(initialTemplate ?? '')
  const [name, setName] = useState(() => (mode === 'symlink' && target ? baseName(target) : ''))
  const [linkTarget, setLinkTarget] = useState<string>(target ?? '')
  const [showPicker, setShowPicker] = useState(!target)
  const [error, setError] = useState<string | null>(null)
  const tpl = template || templates.data?.[0] || ''
  const effectiveName =
    mode === 'template' && name === '' && tpl ? `${t('fileops:new.defaultStem')}.${tpl}` : name
  const check = validateName(effectiveName.trim())
  const reasonText = check.error
    ? lt(`errors:reason.${check.error}`, { defaultValue: '' }) || lt('errors:NAME_INVALID')
    : null

  const mutation = useMutation({
    mutationFn: async (n: string) => {
      switch (mode) {
        case 'folder':
          return mkdir(dir, n)
        case 'file':
          return createFile(dir, n)
        case 'template':
          return createFile(dir, n, tpl)
        case 'symlink': {
          if (!onSymlink) throw new Error('no symlink handler')
          const res = (await onSymlink(joinPath(dir, n), linkTarget)) as { item?: Item } | null
          return res ? { item: res.item!, warnings: [] } : null
        }
      }
    },
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ['list', dir] })
      if (!res) return
      if (mode !== 'symlink') {
        toast.success(t('files:dialog.created', { name: res.item.name }))
        if (res.warnings.includes('windows_incompatible'))
          toast.warning(t('files:dialog.windowsIncompatible'))
        announce(t('files:dialog.created', { name: res.item.name }))
      }
      onCreated?.(res.item)
      onClose()
    },
    onError: (e) => setError(errorMessage(lt, e)),
  })

  const titles: Record<NewItemMode, string> = {
    file: t('files:dialog.newFileTitle'),
    folder: t('files:dialog.newFolderTitle'),
    template: t('fileops:new.templateTitle'),
    symlink: t('fileops:new.symlinkTitle'),
  }
  const canSubmit =
    !!effectiveName.trim() &&
    !check.error &&
    !mutation.isPending &&
    (mode !== 'symlink' || !!linkTarget)

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent
        className="sm:max-w-md"
        data-testid="new-item-dialog"
        onCloseAutoFocus={onCloseAutoFocus}
      >
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (canSubmit) mutation.mutate(effectiveName.trim())
          }}
        >
          <DialogHeader>
            <DialogTitle>{titles[mode]}</DialogTitle>
            <DialogDescription className="font-mono text-xs">{dir}</DialogDescription>
          </DialogHeader>
          <div className="mt-4 space-y-3">
            {mode === 'template' ? (
              <div className="space-y-1">
                <Label htmlFor="new-item-template">{t('fileops:new.template')}</Label>
                <Select value={tpl} onValueChange={setTemplate} disabled={!templates.data}>
                  <SelectTrigger id="new-item-template" data-testid="new-item-template">
                    <SelectValue placeholder={t('fileops:new.loadingTemplates')} />
                  </SelectTrigger>
                  <SelectContent>
                    {(templates.data ?? []).map((name) => (
                      <SelectItem key={name} value={name}>
                        {`.${name}`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ) : null}
            <div className="space-y-1">
              <Label htmlFor="new-item-name">{t('files:dialog.nameLabel')}</Label>
              <Input
                id="new-item-name"
                value={effectiveName}
                onChange={(e) => {
                  setName(e.target.value)
                  setError(null)
                }}
                placeholder={
                  mode === 'folder'
                    ? t('files:dialog.folderPlaceholder')
                    : t('files:dialog.filePlaceholder')
                }
                autoFocus
                autoComplete="off"
                spellCheck={false}
                aria-invalid={error || (effectiveName && check.error) ? true : undefined}
                aria-describedby="new-item-hint"
                data-testid="new-item-name"
              />
              <p
                id="new-item-hint"
                className="min-h-4 text-xs"
                role={error || (effectiveName && check.error) ? 'alert' : undefined}
              >
                {error ? (
                  <span className="text-danger">{error}</span>
                ) : effectiveName && check.error ? (
                  <span className="text-danger">{reasonText}</span>
                ) : check.windowsIncompatible ? (
                  <span className="text-warning">{t('fileops:name.windowsWarning')}</span>
                ) : null}
              </p>
            </div>
            {mode === 'symlink' ? (
              <div className="space-y-1">
                <div className="flex items-center justify-between">
                  <Label htmlFor="new-item-target">{t('fileops:new.target')}</Label>
                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    onClick={() => setShowPicker((v) => !v)}
                  >
                    {showPicker ? t('fileops:new.hidePicker') : t('fileops:new.pickTarget')}
                  </Button>
                </div>
                <Input
                  id="new-item-target"
                  value={linkTarget}
                  onChange={(e) => setLinkTarget(e.target.value)}
                  className="font-mono text-xs"
                  spellCheck={false}
                  autoComplete="off"
                  placeholder="/folder/file.txt"
                  data-testid="new-item-target"
                />
                {showPicker ? (
                  <FolderPicker
                    value={linkTarget || null}
                    onChange={(p) => {
                      setLinkTarget(p)
                      if (!name) setName(baseName(p))
                    }}
                    mode="all"
                    className="max-h-48"
                  />
                ) : null}
                {linkTarget ? (
                  <p className="text-xs text-text-muted">
                    {t('fileops:new.relative', { rel: relativeTarget(dir, linkTarget) })}
                  </p>
                ) : null}
              </div>
            ) : null}
          </div>
          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common:actions.cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={!canSubmit}
              data-testid="new-item-submit"
            >
              {t('files:dialog.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
