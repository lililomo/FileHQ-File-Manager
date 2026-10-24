import { FolderPlus } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
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
import { mkdir } from '@/features/browser/api'
import { normalizeTypedPath, ROOT } from '@/features/browser/path'
import { formatNumber } from '@/lib/format'
import type { TransferOp } from './api'
import { canDropOn } from './dnd'
import { FolderPicker, type FolderPickerHandle } from './FolderPicker'
import { errorMessage } from './useFileOps'

export interface MoveCopyDialogProps {
  op: TransferOp
  paths: string[]
  from: string
  onClose: () => void
  onSubmit: (to: string) => void
  onCloseAutoFocus?: (e: Event) => void
}

/** "Pindah ke… / Salin ke…" (SPEC §8.5): lazy folder tree + typed path +
 *  "Folder baru di sini"; destinations inside a moved item are rejected
 *  client-side (the server checks again). */
export function MoveCopyDialog({
  op,
  paths,
  from,
  onClose,
  onSubmit,
  onCloseAutoFocus,
}: MoveCopyDialogProps) {
  const { t, i18n } = useTranslation(['fileops', 'errors'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const [dest, setDest] = useState(from)
  const [typed, setTyped] = useState(from)
  const [typedError, setTypedError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const picker = useRef<FolderPickerHandle>(null)

  const choose = (p: string) => {
    setDest(p)
    setTyped(p)
    setTypedError(null)
    setError(null)
  }
  const applyTyped = (): string | null => {
    const r = normalizeTypedPath(typed, ROOT)
    if (!r.ok) {
      setTypedError(t('fileops:moveCopy.invalidPath'))
      return null
    }
    setDest(r.path)
    setTyped(r.path)
    setTypedError(null)
    return r.path
  }

  // validate the live typed path so Enter in the field can submit at once
  const typedNorm = normalizeTypedPath(typed, ROOT)
  const candidate = typedNorm.ok ? typedNorm.path : dest
  const intoSelf = !canDropOn(paths, candidate)
  const sameFolder = op === 'move' && candidate === from
  const blocked = intoSelf
    ? t('fileops:moveCopy.intoSelf')
    : sameFolder
      ? t('fileops:moveCopy.sameFolder')
      : null

  const createFolder = async () => {
    const name = newName.trim()
    if (!name) return
    try {
      const res = await mkdir(dest, name)
      setCreating(false)
      setNewName('')
      await picker.current?.reload(dest)
      choose(res.item.path)
    } catch (e) {
      setError(errorMessage(t as unknown as (k: string, o?: Record<string, unknown>) => string, e))
    }
  }

  const submit = () => {
    const p = applyTyped()
    if (!p) return
    if (!canDropOn(paths, p) || (op === 'move' && p === from)) return
    onSubmit(p)
  }
  const count = paths.length

  return (
    <Dialog open onOpenChange={(o) => (!o ? onClose() : undefined)}>
      <DialogContent
        className="sm:max-w-md"
        data-testid="movecopy-dialog"
        onCloseAutoFocus={onCloseAutoFocus}
        onOpenAutoFocus={(e) => {
          // start in the tree (keyboard-first), not in the path input
          e.preventDefault()
          ;(e.currentTarget as HTMLElement).querySelector<HTMLElement>('[role="tree"]')?.focus()
        }}
      >
        <DialogHeader>
          <DialogTitle>
            {t(op === 'move' ? 'fileops:moveCopy.titleMove' : 'fileops:moveCopy.titleCopy')}
          </DialogTitle>
          <DialogDescription>
            {t('fileops:moveCopy.items', { count, n: formatNumber(count, lang) })}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div className="space-y-1">
            <Label htmlFor="movecopy-path">{t('fileops:moveCopy.destination')}</Label>
            <Input
              id="movecopy-path"
              value={typed}
              onChange={(e) => {
                setTyped(e.target.value)
                setTypedError(null)
              }}
              onBlur={() => {
                if (typed !== dest) applyTyped()
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  submit()
                }
              }}
              className="font-mono text-xs"
              spellCheck={false}
              autoComplete="off"
              aria-invalid={typedError ? true : undefined}
              aria-describedby={typedError ? 'movecopy-path-error' : undefined}
              data-testid="movecopy-path"
            />
            {typedError ? (
              <p className="text-xs text-danger" role="alert" id="movecopy-path-error">
                {typedError}
              </p>
            ) : null}
          </div>
          <FolderPicker
            ref={picker}
            value={dest}
            onChange={choose}
            isDisabled={(p) => !canDropOn(paths, p)}
            onActivate={() => submit()}
          />
          {creating ? (
            <div className="flex items-center gap-2">
              <Input
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder={t('fileops:moveCopy.newFolderPlaceholder')}
                autoFocus
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    void createFolder()
                  }
                  if (e.key === 'Escape') {
                    e.stopPropagation()
                    setCreating(false)
                  }
                }}
                data-testid="movecopy-newfolder-name"
              />
              <Button
                type="button"
                size="sm"
                onClick={() => void createFolder()}
                data-testid="movecopy-newfolder-ok"
              >
                {t('fileops:moveCopy.create')}
              </Button>
            </div>
          ) : (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="gap-1"
              onClick={() => setCreating(true)}
              data-testid="movecopy-newfolder"
            >
              <FolderPlus aria-hidden="true" />
              {t('fileops:moveCopy.newFolderHere')}
            </Button>
          )}
          {blocked ? (
            <p className="text-xs text-danger" role="alert" data-testid="movecopy-blocked">
              {blocked}
            </p>
          ) : null}
          {error ? (
            <p className="text-xs text-danger" role="alert">
              {error}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('fileops:cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={!!blocked}
              data-testid="movecopy-submit"
            >
              {t(op === 'move' ? 'fileops:moveCopy.submitMove' : 'fileops:moveCopy.submitCopy')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
