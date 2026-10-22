import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
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
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { ApiError } from '@/lib/api'
import { errorMessage } from '@/lib/errors'
import { formatBytes } from '@/lib/format'
import {
  createUser,
  deleteUser,
  updateUser,
  type AdminUser,
  type CreateUserResult,
  type DeleteMode,
} from './api'

const GB = 1024 * 1024 * 1024
const MB = 1024 * 1024

const USERNAME_RE = /^[a-z0-9][a-z0-9_-]{2,31}$/

/** Shared field error → message (server `details.fields` reasons win). */
function useFieldError() {
  const { t } = useTranslation('admin')
  return (reason: string | undefined): string | undefined => {
    if (!reason) return undefined
    const key = `users.form.errors.${reason}`
    const msg = t(key, { defaultValue: '' })
    return msg || reason
  }
}

/** Maps an ApiError onto form fields (VALIDATION_FAILED fields, ALREADY_EXISTS). */
function applyApiError(
  e: unknown,
  setError: (name: string, err: { message: string }) => void,
  fieldOf: (reason: string | undefined) => string | undefined,
): string | null {
  if (!(e instanceof ApiError)) return errorMessage(e)
  const fields = e.fields
  const names = Object.keys(fields)
  if (names.length > 0) {
    for (const name of names)
      setError(name, { message: fieldOf(fields[name]) ?? fields[name] ?? '' })
    return null
  }
  return errorMessage(e)
}

// ---------------------------------------------------------------------------
// Create

export interface CreateUserDialogProps {
  defaultQuotaBytes: number
  onClose: () => void
  onCreated: (res: CreateUserResult) => void
}

type CreateValues = {
  username: string
  displayName: string
  role: 'admin' | 'user'
  quotaGb: number
  passwordMode: 'generate' | 'manual'
  temporaryPassword: string
}

export function CreateUserDialog({ defaultQuotaBytes, onClose, onCreated }: CreateUserDialogProps) {
  const { t, i18n } = useTranslation('admin')
  const { t: tc } = useTranslation('common')
  const lang = i18n.resolvedLanguage ?? 'id'
  const fieldOf = useFieldError()
  const id = useId()
  const schema = useMemo(
    () =>
      z
        .object({
          username: z.string().regex(USERNAME_RE, 'invalid'),
          displayName: z.string().max(80, 'max'),
          role: z.enum(['admin', 'user']),
          quotaGb: z.number().min(0.001, 'min'),
          passwordMode: z.enum(['generate', 'manual']),
          temporaryPassword: z.string(),
        })
        .refine((v) => v.passwordMode === 'generate' || v.temporaryPassword.length >= 12, {
          path: ['temporaryPassword'],
          message: 'too_short',
        }),
    [],
  )
  const {
    register,
    handleSubmit,
    setError,
    setValue,
    control,
    formState: { errors, isSubmitting },
  } = useForm<CreateValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      username: '',
      displayName: '',
      role: 'user',
      quotaGb: Math.round((defaultQuotaBytes / GB) * 100) / 100,
      passwordMode: 'generate',
      temporaryPassword: '',
    },
    mode: 'onBlur',
  })
  const [formError, setFormError] = useState<string | null>(null)
  const role = useWatch({ control, name: 'role' })
  const passwordMode = useWatch({ control, name: 'passwordMode' })
  const submit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      const res = await createUser({
        username: v.username,
        displayName: v.displayName || v.username,
        role: v.role,
        quotaBytes: Math.max(MB, Math.round(v.quotaGb * GB)),
        ...(v.passwordMode === 'manual' ? { temporaryPassword: v.temporaryPassword } : {}),
      })
      onCreated(res)
    } catch (e) {
      const msg = applyApiError(e, (n, err) => setError(n as keyof CreateValues, err), fieldOf)
      if (msg) setFormError(msg)
    }
  })
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent data-testid="user-create-dialog">
        <DialogHeader>
          <DialogTitle>{t('users.form.createTitle')}</DialogTitle>
          <DialogDescription>{t('users.form.passwordHint')}</DialogDescription>
        </DialogHeader>
        <form onSubmit={(e) => void submit(e)} className="space-y-3" noValidate>
          <div className="space-y-1">
            <Label htmlFor={`${id}-username`}>{t('users.form.username')}</Label>
            <Input
              id={`${id}-username`}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={!!errors.username}
              data-testid="user-username"
              {...register('username')}
            />
            <p className="text-xs text-text-muted">{t('users.form.usernameHint')}</p>
            {errors.username ? (
              <p className="text-xs text-danger" role="alert" data-testid="user-username-error">
                {fieldOf(errors.username.message)}
              </p>
            ) : null}
          </div>
          <div className="space-y-1">
            <Label htmlFor={`${id}-display`}>{t('users.form.displayName')}</Label>
            <Input
              id={`${id}-display`}
              data-testid="user-displayname"
              {...register('displayName')}
            />
            {errors.displayName ? (
              <p className="text-xs text-danger" role="alert">
                {fieldOf(errors.displayName.message)}
              </p>
            ) : null}
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label htmlFor={`${id}-role`}>{t('users.form.role')}</Label>
              <Select value={role} onValueChange={(v) => setValue('role', v as 'admin' | 'user')}>
                <SelectTrigger id={`${id}-role`} data-testid="user-role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="user">{t('users.roleUser')}</SelectItem>
                  <SelectItem value="admin">{t('users.roleAdmin')}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label htmlFor={`${id}-quota`}>
                {t('users.form.quota')} ({t('users.form.quotaUnit')})
              </Label>
              <Input
                id={`${id}-quota`}
                type="number"
                step="0.01"
                min="0.001"
                data-testid="user-quota"
                {...register('quotaGb', { valueAsNumber: true })}
              />
              <p className="text-xs text-text-muted">
                {t('users.form.quotaHint', { default: formatBytes(defaultQuotaBytes, lang) })}
              </p>
              {errors.quotaGb ? (
                <p className="text-xs text-danger" role="alert">
                  {fieldOf(errors.quotaGb.message)}
                </p>
              ) : null}
            </div>
          </div>
          <fieldset className="space-y-1">
            <legend className="text-sm font-medium">{t('users.form.password')}</legend>
            <RadioGroup
              value={passwordMode}
              onValueChange={(v) => setValue('passwordMode', v as 'generate' | 'manual')}
              className="flex gap-4"
            >
              <label className="flex items-center gap-2 text-sm">
                <RadioGroupItem value="generate" data-testid="user-password-generate" />
                {t('users.form.passwordGenerate')}
              </label>
              <label className="flex items-center gap-2 text-sm">
                <RadioGroupItem value="manual" data-testid="user-password-manual" />
                {t('users.form.passwordManual')}
              </label>
            </RadioGroup>
            {passwordMode === 'manual' ? (
              <>
                <Input
                  type="password"
                  autoComplete="new-password"
                  aria-label={t('users.form.password')}
                  data-testid="user-password"
                  {...register('temporaryPassword')}
                />
                {errors.temporaryPassword ? (
                  <p className="text-xs text-danger" role="alert">
                    {fieldOf(errors.temporaryPassword.message)}
                  </p>
                ) : null}
              </>
            ) : null}
          </fieldset>
          {formError ? (
            <p className="text-sm text-danger" role="alert">
              {formError}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {tc('actions.cancel')}
            </Button>
            <Button type="submit" disabled={isSubmitting} data-testid="user-create-submit">
              {t('users.form.submitCreate')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// Edit

export interface EditUserDialogProps {
  user: AdminUser
  isSelf: boolean
  onClose: () => void
  onSaved: (user: AdminUser) => void
}

type EditValues = {
  displayName: string
  role: 'admin' | 'user'
  quotaGb: number
  status: 'active' | 'disabled'
}

export function EditUserDialog({ user, isSelf, onClose, onSaved }: EditUserDialogProps) {
  const { t } = useTranslation('admin')
  const { t: tc } = useTranslation('common')
  const fieldOf = useFieldError()
  const id = useId()
  const schema = useMemo(
    () =>
      z.object({
        displayName: z.string().min(1, 'required').max(80, 'max'),
        role: z.enum(['admin', 'user']),
        quotaGb: z.number().min(0.001, 'min'),
        status: z.enum(['active', 'disabled']),
      }),
    [],
  )
  const {
    register,
    handleSubmit,
    setError,
    setValue,
    control,
    formState: { errors, isSubmitting },
  } = useForm<EditValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      displayName: user.displayName,
      role: user.role,
      quotaGb: Math.round((user.quota.limit / GB) * 100) / 100,
      status: user.status,
    },
  })
  const [formError, setFormError] = useState<string | null>(null)
  const role = useWatch({ control, name: 'role' })
  const status = useWatch({ control, name: 'status' })
  const submit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      const res = await updateUser(user.id, {
        displayName: v.displayName,
        role: v.role,
        quotaBytes: Math.max(MB, Math.round(v.quotaGb * GB)),
        status: v.status,
      })
      onSaved(res.user)
    } catch (e) {
      const msg = applyApiError(e, (n, err) => setError(n as keyof EditValues, err), fieldOf)
      if (msg) setFormError(msg)
    }
  })
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent data-testid="user-edit-dialog">
        <DialogHeader>
          <DialogTitle>{t('users.form.editTitle', { username: user.username })}</DialogTitle>
        </DialogHeader>
        <form onSubmit={(e) => void submit(e)} className="space-y-3" noValidate>
          <div className="space-y-1">
            <Label htmlFor={`${id}-display`}>{t('users.form.displayName')}</Label>
            <Input
              id={`${id}-display`}
              data-testid="user-displayname"
              {...register('displayName')}
            />
            {errors.displayName ? (
              <p className="text-xs text-danger" role="alert">
                {fieldOf(errors.displayName.message)}
              </p>
            ) : null}
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label htmlFor={`${id}-role`}>{t('users.form.role')}</Label>
              <Select value={role} onValueChange={(v) => setValue('role', v as 'admin' | 'user')}>
                <SelectTrigger id={`${id}-role`} data-testid="user-role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="user">{t('users.roleUser')}</SelectItem>
                  <SelectItem value="admin">{t('users.roleAdmin')}</SelectItem>
                </SelectContent>
              </Select>
              {errors.role ? (
                <p className="text-xs text-danger" role="alert" data-testid="user-role-error">
                  {fieldOf(errors.role.message)}
                </p>
              ) : null}
            </div>
            <div className="space-y-1">
              <Label htmlFor={`${id}-quota`}>
                {t('users.form.quota')} ({t('users.form.quotaUnit')})
              </Label>
              <Input
                id={`${id}-quota`}
                type="number"
                step="0.001"
                min="0.001"
                data-testid="user-quota"
                {...register('quotaGb', { valueAsNumber: true })}
              />
              {errors.quotaGb ? (
                <p className="text-xs text-danger" role="alert">
                  {fieldOf(errors.quotaGb.message)}
                </p>
              ) : null}
            </div>
          </div>
          <div className="space-y-1">
            <Label htmlFor={`${id}-status`}>{t('users.form.status')}</Label>
            <Select
              value={status}
              onValueChange={(v) => setValue('status', v as 'active' | 'disabled')}
              disabled={isSelf}
            >
              <SelectTrigger id={`${id}-status`} data-testid="user-status">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="active">{t('users.statusActive')}</SelectItem>
                <SelectItem value="disabled">{t('users.statusDisabled')}</SelectItem>
              </SelectContent>
            </Select>
            {errors.status ? (
              <p className="text-xs text-danger" role="alert">
                {fieldOf(errors.status.message)}
              </p>
            ) : null}
          </div>
          {formError ? (
            <p className="text-sm text-danger" role="alert" data-testid="user-edit-error">
              {formError}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {tc('actions.cancel')}
            </Button>
            <Button type="submit" disabled={isSubmitting} data-testid="user-edit-submit">
              {t('users.form.submitEdit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// One-time password

export function PasswordOnceDialog({
  username,
  password,
  onClose,
}: {
  username: string
  password: string
  onClose: () => void
}) {
  const { t } = useTranslation('admin')
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(password)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent data-testid="password-once-dialog">
        <DialogHeader>
          <DialogTitle>{t('users.passwordOnce.title', { username })}</DialogTitle>
          <DialogDescription>{t('users.passwordOnce.body')}</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <code
            className="flex-1 rounded-md border border-border bg-surface-2 px-3 py-2 font-mono text-base select-all"
            data-testid="temporary-password"
          >
            {password}
          </code>
          <Button
            type="button"
            variant="outline"
            onClick={() => void copy()}
            data-testid="password-copy"
          >
            {copied ? (
              <Check className="size-4" aria-hidden="true" />
            ) : (
              <Copy className="size-4" aria-hidden="true" />
            )}
            {copied ? t('users.passwordOnce.copied') : t('users.passwordOnce.copy')}
          </Button>
        </div>
        <DialogFooter>
          <Button type="button" onClick={onClose} data-testid="password-done">
            {t('users.passwordOnce.done')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// Delete

export function DeleteUserDialog({
  user,
  onClose,
  onDeleted,
}: {
  user: AdminUser
  onClose: () => void
  onDeleted: () => void
}) {
  const { t } = useTranslation('admin')
  const { t: tc } = useTranslation('common')
  const id = useId()
  const [confirm, setConfirm] = useState('')
  const [mode, setMode] = useState<DeleteMode>('archive')
  const run = useMutation({
    mutationFn: () => deleteUser(user.id, confirm, mode),
    onSuccess: onDeleted,
  })
  return (
    <AlertDialog open onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent data-testid="user-delete-dialog">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t('users.delete.title', { username: user.username })}
          </AlertDialogTitle>
          <AlertDialogDescription>{t('users.delete.body')}</AlertDialogDescription>
        </AlertDialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor={`${id}-confirm`}>
              {t('users.delete.confirmLabel', { username: user.username })}
            </Label>
            <Input
              id={`${id}-confirm`}
              autoComplete="off"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              data-testid="user-delete-confirm"
            />
          </div>
          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">{t('users.delete.mode')}</legend>
            <RadioGroup value={mode} onValueChange={(v) => setMode(v as DeleteMode)}>
              <label className="flex items-start gap-2 text-sm">
                <RadioGroupItem
                  value="archive"
                  className="mt-0.5"
                  data-testid="user-delete-archive"
                />
                <span>
                  <span className="font-medium">{t('users.delete.archive')}</span>
                  <span className="block text-xs text-text-muted">
                    {t('users.delete.archiveHint', { username: user.username })}
                  </span>
                </span>
              </label>
              <label className="flex items-start gap-2 text-sm">
                <RadioGroupItem value="purge" className="mt-0.5" data-testid="user-delete-purge" />
                <span>
                  <span className="font-medium">{t('users.delete.purge')}</span>
                  <span className="block text-xs text-text-muted">
                    {t('users.delete.purgeHint')}
                  </span>
                </span>
              </label>
            </RadioGroup>
          </fieldset>
          {run.isError ? (
            <p className="text-sm text-danger" role="alert">
              {errorMessage(run.error)}
            </p>
          ) : null}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>{tc('actions.cancel')}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={confirm !== user.username || run.isPending}
            onClick={(e) => {
              e.preventDefault()
              run.mutate()
            }}
            data-testid="user-delete-submit"
          >
            {t('users.delete.submit')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** Generic confirm for disable / revoke / reset. */
export function ConfirmActionDialog({
  title,
  body,
  destructive,
  pending,
  onCancel,
  onConfirm,
  testId,
}: {
  title: string
  body: string
  destructive?: boolean
  pending?: boolean
  onCancel: () => void
  onConfirm: () => void
  testId: string
}) {
  const { t } = useTranslation('common')
  return (
    <AlertDialog open onOpenChange={(open) => !open && onCancel()}>
      <AlertDialogContent data-testid={testId}>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{body}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t('actions.cancel')}</AlertDialogCancel>
          <AlertDialogAction
            variant={destructive ? 'destructive' : 'default'}
            disabled={pending}
            onClick={(e) => {
              e.preventDefault()
              onConfirm()
            }}
            data-testid={`${testId}-confirm`}
          >
            {t('actions.continue')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
