import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { MoreHorizontal, Plus, Users } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { errorMessage } from '@/lib/errors'
import { formatBytes, formatDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/stores/session'
import {
  adminKeys,
  listUsers,
  resetPassword,
  resetTotp,
  revokeSessions,
  setUserStatus,
  type AdminUser,
  type AdminUserPage,
  type UserStatus,
} from './api'
import {
  ConfirmActionDialog,
  CreateUserDialog,
  DeleteUserDialog,
  EditUserDialog,
  PasswordOnceDialog,
} from './UserDialogs'

type RoleFilter = '' | 'admin' | 'user'
type StatusFilter = '' | UserStatus
type Pending =
  | { kind: 'create' }
  | { kind: 'edit'; user: AdminUser }
  | { kind: 'password'; username: string; password: string }
  | { kind: 'delete'; user: AdminUser }
  | { kind: 'disable' | 'revoke' | 'reset' | 'resetTotp'; user: AdminUser }
  | null

/** `/admin/users` (SPEC §8.17 "Users"): dense table with filters, quota bar
 *  and a per-row action menu; every action is confirmed and the server rules
 *  (last admin, self) come back as messages. */
export function UsersPage() {
  const { t, i18n } = useTranslation(['admin', 'common'])
  const lang = i18n.resolvedLanguage ?? 'id'
  const qc = useQueryClient()
  const navigate = useNavigate()
  const me = useSessionStore((s) => s.user)
  const config = useSessionStore((s) => s.config)
  const [q, setQ] = useState('')
  const [role, setRole] = useState<RoleFilter>('')
  const [status, setStatus] = useState<StatusFilter>('')
  const [pending, setPending] = useState<Pending>(null)
  const query = useInfiniteQuery<
    AdminUserPage,
    Error,
    { pages: AdminUserPage[] },
    readonly unknown[],
    string | undefined
  >({
    queryKey: [...adminKeys.users, q, role, status],
    queryFn: ({ pageParam, signal }) =>
      listUsers({ q, role, status, ...(pageParam ? { cursor: pageParam } : {}) }, signal),
    initialPageParam: undefined,
    getNextPageParam: (last) => last.nextCursor ?? undefined,
  })
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data])
  const total = query.data?.pages[0]?.total ?? 0
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: adminKeys.users })
    void qc.invalidateQueries({ queryKey: adminKeys.dashboard })
  }
  const action = useMutation({
    mutationFn: async ({
      kind,
      user,
    }: {
      kind: 'disable' | 'enable' | 'revoke' | 'reset' | 'resetTotp'
      user: AdminUser
    }) => {
      if (kind === 'reset')
        return { kind, user, password: (await resetPassword(user.id)).temporaryPassword }
      if (kind === 'revoke') {
        await revokeSessions(user.id)
        return { kind, user, password: '' }
      }
      if (kind === 'resetTotp') {
        await resetTotp(user.id)
        return { kind, user, password: '' }
      }
      await setUserStatus(user.id, kind === 'disable' ? 'disabled' : 'active')
      return { kind, user, password: '' }
    },
    onSuccess: ({ kind, user, password }) => {
      invalidate()
      if (kind === 'reset') {
        setPending({ kind: 'password', username: user.username, password })
        return
      }
      setPending(null)
      toast.success(
        t(
          kind === 'disable'
            ? 'admin:users.toast.disabled'
            : kind === 'enable'
              ? 'admin:users.toast.enabled'
              : kind === 'resetTotp'
                ? 'admin:users.toast.totpReset'
                : 'admin:users.toast.revoked',
          { username: user.username },
        ),
      )
    },
    onError: (e) => {
      setPending(null)
      toast.error(errorMessage(e))
    },
  })
  const canBrowse = config?.adminCanBrowseUsers === true

  return (
    <div className="flex h-full flex-col" data-testid="admin-users">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <h1 className="text-lg font-semibold">{t('admin:users.title')}</h1>
        <span className="text-sm text-text-muted" data-testid="users-total">
          {t('admin:users.total', { count: total })}
        </span>
        <span className="flex-1" />
        <Input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t('admin:users.search')}
          aria-label={t('admin:users.search')}
          className="h-8 w-56"
          data-testid="users-search"
        />
        <Select
          value={role || 'all'}
          onValueChange={(v) => setRole(v === 'all' ? '' : (v as RoleFilter))}
        >
          <SelectTrigger
            className="h-8 w-36"
            aria-label={t('admin:users.colRole')}
            data-testid="users-filter-role"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('admin:users.filterRole')}</SelectItem>
            <SelectItem value="admin">{t('admin:users.roleAdmin')}</SelectItem>
            <SelectItem value="user">{t('admin:users.roleUser')}</SelectItem>
          </SelectContent>
        </Select>
        <Select
          value={status || 'all'}
          onValueChange={(v) => setStatus(v === 'all' ? '' : (v as StatusFilter))}
        >
          <SelectTrigger
            className="h-8 w-36"
            aria-label={t('admin:users.colStatus')}
            data-testid="users-filter-status"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('admin:users.filterStatus')}</SelectItem>
            <SelectItem value="active">{t('admin:users.statusActive')}</SelectItem>
            <SelectItem value="disabled">{t('admin:users.statusDisabled')}</SelectItem>
          </SelectContent>
        </Select>
        <Button size="sm" onClick={() => setPending({ kind: 'create' })} data-testid="users-create">
          <Plus className="size-4" aria-hidden="true" />
          {t('admin:users.create')}
        </Button>
      </header>
      {query.isPending ? (
        <div className="space-y-2 p-3" aria-busy="true">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : query.isError ? (
        <EmptyState icon={Users} title={errorMessage(query.error)} />
      ) : rows.length === 0 ? (
        <EmptyState icon={Users} title={t('admin:users.empty')} />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <Table data-testid="users-table">
            <TableHeader>
              <TableRow>
                <TableHead>{t('admin:users.colUser')}</TableHead>
                <TableHead>{t('admin:users.colRole')}</TableHead>
                <TableHead>{t('admin:users.colStatus')}</TableHead>
                <TableHead className="w-56">{t('admin:users.colQuota')}</TableHead>
                <TableHead>{t('admin:users.colLastLogin')}</TableHead>
                <TableHead>{t('admin:users.colHome')}</TableHead>
                <TableHead>{t('admin:users.colCreated')}</TableHead>
                <TableHead className="w-12 text-right">{t('admin:users.colActions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((u) => {
                const used = u.quota.used + u.quota.trash + u.quota.versions
                const pct = u.quota.limit > 0 ? Math.min(100, (used / u.quota.limit) * 100) : 0
                const isSelf = me?.id === u.id
                return (
                  <TableRow
                    key={u.id}
                    data-testid="user-row"
                    data-username={u.username}
                    data-status={u.status}
                  >
                    <TableCell>
                      <div className="font-medium">
                        {u.username}
                        {isSelf ? (
                          <span className="ml-1 text-xs text-text-muted">
                            ({t('admin:users.you')})
                          </span>
                        ) : null}
                      </div>
                      <div className="text-xs text-text-muted">{u.displayName}</div>
                    </TableCell>
                    <TableCell>
                      <Badge variant={u.role === 'admin' ? 'default' : 'secondary'}>
                        {t(u.role === 'admin' ? 'admin:users.roleAdmin' : 'admin:users.roleUser')}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        <Badge
                          variant={u.status === 'active' ? 'outline' : 'destructive'}
                          className={u.status === 'active' ? 'text-success' : ''}
                          data-testid="user-status-badge"
                        >
                          {t(
                            u.status === 'active'
                              ? 'admin:users.statusActive'
                              : 'admin:users.statusDisabled',
                          )}
                        </Badge>
                        {u.mustChangePassword ? (
                          <Badge variant="outline">{t('admin:users.mustChange')}</Badge>
                        ) : null}
                        {u.lockedUntil ? (
                          <Badge variant="outline">{t('admin:users.locked')}</Badge>
                        ) : null}
                        {u.totpEnabled ? (
                          <Badge
                            variant="outline"
                            className="text-success"
                            data-testid="user-twofa-badge"
                          >
                            {t('admin:users.twofa')}
                          </Badge>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="text-xs tabular">
                        {t('admin:users.quotaOf', {
                          used: formatBytes(used, lang),
                          limit: formatBytes(u.quota.limit, lang),
                        })}
                      </div>
                      <div
                        className="mt-1 h-1.5 w-full overflow-hidden rounded bg-surface-2"
                        role="progressbar"
                        aria-valuenow={Math.round(pct)}
                        aria-valuemin={0}
                        aria-valuemax={100}
                        aria-label={t('admin:users.colQuota')}
                      >
                        <div
                          className={cn(
                            'h-full',
                            pct >= 95 ? 'bg-danger' : pct >= 80 ? 'bg-warning' : 'bg-accent',
                          )}
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                    </TableCell>
                    <TableCell className="text-xs">
                      {u.lastLoginAt ? formatDate(u.lastLoginAt, lang) : t('admin:users.never')}
                    </TableCell>
                    <TableCell className="text-xs">
                      {u.homeWarning ? (
                        <span className="text-warning">{t('admin:users.homeWarning')}</span>
                      ) : u.homeProvisioned ? (
                        t('admin:users.homeReady')
                      ) : (
                        <span className="text-text-muted">{t('admin:users.homePending')}</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs">{formatDate(u.createdAt, lang)}</TableCell>
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={t('common:actions.openMenu')}
                            data-testid="user-menu"
                          >
                            <MoreHorizontal className="size-4" aria-hidden="true" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            onSelect={() => setPending({ kind: 'edit', user: u })}
                            data-testid="user-action-edit"
                          >
                            {t('admin:users.actions.edit')}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onSelect={() => setPending({ kind: 'reset', user: u })}
                            data-testid="user-action-reset"
                          >
                            {t('admin:users.actions.resetPassword')}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onSelect={() => setPending({ kind: 'revoke', user: u })}
                            data-testid="user-action-revoke"
                          >
                            {t('admin:users.actions.revokeSessions')}
                          </DropdownMenuItem>
                          {u.totpEnabled ? (
                            <DropdownMenuItem
                              onSelect={() => setPending({ kind: 'resetTotp', user: u })}
                              data-testid="user-action-reset-totp"
                            >
                              {t('admin:users.actions.resetTotp')}
                            </DropdownMenuItem>
                          ) : null}
                          {u.status === 'active' ? (
                            <DropdownMenuItem
                              disabled={isSelf}
                              onSelect={() => setPending({ kind: 'disable', user: u })}
                              data-testid="user-action-disable"
                            >
                              {t('admin:users.actions.disable')}
                            </DropdownMenuItem>
                          ) : (
                            <DropdownMenuItem
                              onSelect={() => action.mutate({ kind: 'enable', user: u })}
                              data-testid="user-action-enable"
                            >
                              {t('admin:users.actions.enable')}
                            </DropdownMenuItem>
                          )}
                          {canBrowse ? (
                            <DropdownMenuItem
                              onSelect={() =>
                                void navigate({
                                  to: '/admin/users/$username/files/$',
                                  params: { username: u.username, _splat: '' },
                                })
                              }
                              data-testid="user-action-browse"
                            >
                              {t('admin:users.actions.browse')}
                            </DropdownMenuItem>
                          ) : null}
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            variant="destructive"
                            disabled={isSelf}
                            onSelect={() => setPending({ kind: 'delete', user: u })}
                            data-testid="user-action-delete"
                          >
                            {t('admin:users.actions.delete')}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
          {query.hasNextPage ? (
            <div className="p-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
              >
                {t('admin:users.loadMore')}
              </Button>
            </div>
          ) : null}
        </div>
      )}

      {pending?.kind === 'create' ? (
        <CreateUserDialog
          defaultQuotaBytes={10 * 1024 * 1024 * 1024}
          onClose={() => setPending(null)}
          onCreated={(res) => {
            invalidate()
            toast.success(t('admin:users.toast.created', { username: res.user.username }))
            if (res.warnings.includes('home_exists'))
              toast.warning(t('admin:users.toast.homeExists', { username: res.user.username }))
            setPending({
              kind: 'password',
              username: res.user.username,
              password: res.temporaryPassword,
            })
          }}
        />
      ) : null}
      {pending?.kind === 'edit' ? (
        <EditUserDialog
          user={pending.user}
          isSelf={me?.id === pending.user.id}
          onClose={() => setPending(null)}
          onSaved={() => {
            invalidate()
            toast.success(t('admin:users.toast.updated'))
            setPending(null)
          }}
        />
      ) : null}
      {pending?.kind === 'password' ? (
        <PasswordOnceDialog
          username={pending.username}
          password={pending.password}
          onClose={() => setPending(null)}
        />
      ) : null}
      {pending?.kind === 'delete' ? (
        <DeleteUserDialog
          user={pending.user}
          onClose={() => setPending(null)}
          onDeleted={() => {
            invalidate()
            void qc.invalidateQueries({ queryKey: ['tasks'] })
            toast.success(t('admin:users.toast.deleteQueued', { username: pending.user.username }))
            setPending(null)
          }}
        />
      ) : null}
      {pending?.kind === 'disable' ||
      pending?.kind === 'revoke' ||
      pending?.kind === 'reset' ||
      pending?.kind === 'resetTotp' ? (
        <ConfirmActionDialog
          testId={`user-confirm-${pending.kind}`}
          title={t(`admin:users.confirm.${pending.kind}Title`, { username: pending.user.username })}
          body={t(`admin:users.confirm.${pending.kind}Body`)}
          destructive={pending.kind !== 'reset'}
          pending={action.isPending}
          onCancel={() => setPending(null)}
          onConfirm={() => action.mutate({ kind: pending.kind, user: pending.user })}
        />
      ) : null}
    </div>
  )
}
