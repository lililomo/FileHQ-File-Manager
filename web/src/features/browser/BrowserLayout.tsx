import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { FolderOpen, FolderPlus, Upload } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { announce } from '@/lib/announce'
import { ApiError } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { useIsMobile } from '@/lib/useMediaQuery'
import { useTabsStore } from '@/stores/tabs'
import { AddressBar } from './AddressBar'
import type { Item } from './api'
import { DetailsView } from './DetailsView'
import { DEFAULT_COLUMNS, MOBILE_COLUMNS } from './columns'
import { NewItemDialog, type NewItemKind } from './NewItemDialog'
import { StatusBar } from './StatusBar'
import { Toolbar } from './Toolbar'
import { useBrowserShortcuts } from './useBrowserShortcuts'
import { useListing } from './useListing'
import { baseName, parentPath, pathToSplat, ROOT } from './path'

export function BrowserLayout({ path }: { path: string }) {
  const { t, i18n } = useTranslation(['files', 'errors', 'common'])
  const navigate = useNavigate()
  const qc = useQueryClient()
  const isMobile = useIsMobile()
  const [editing, setEditing] = useState(false)
  const [newKind, setNewKind] = useState<NewItemKind | null>(null)

  const tabs = useTabsStore()
  useEffect(() => {
    useTabsStore.getState().visit(path)
  }, [path])
  const tab = tabs.active()

  const listing = useListing(path)
  const { items, total, isPending, isError, error, refetch, isFetching } = listing

  const go = useCallback(
    (to: string) => void navigate({ to: '/files/$', params: { _splat: pathToSplat(to) } }),
    [navigate],
  )
  const parent = parentPath(path)
  const handlers = useMemo(
    () => ({
      back: () => {
        const p = useTabsStore.getState().back()
        if (p !== null) go(p)
      },
      forward: () => {
        const p = useTabsStore.getState().forward()
        if (p !== null) go(p)
      },
      up: () => {
        if (parent !== null) go(parent)
      },
      refresh: () => {
        void refetch().then(() => announce(t('files:announce.refreshed')))
      },
      editPath: () => setEditing(true),
    }),
    [go, parent, refetch, t],
  )
  useBrowserShortcuts(handlers)

  // Announce once per successful load of a folder.
  const announced = useRef<string | null>(null)
  useEffect(() => {
    if (isPending || isError) return
    const key = `${path}:${total}`
    if (announced.current === key) return
    announced.current = key
    announce(
      t('files:announce.opened', {
        name: path === ROOT ? t('files:nav.root') : baseName(path),
        count: total,
        n: formatNumber(total, i18n.resolvedLanguage ?? 'id'),
      }),
    )
  }, [path, total, isPending, isError, t, i18n.resolvedLanguage])

  const onOpen = useCallback(
    (item: Item) => {
      if (item.isDir || (item.isSymlink && !item.broken && item.type === 'dir')) {
        go(item.path)
        return
      }
      toast(t('files:viewer.notYet', { name: item.name }))
    },
    [go, t],
  )

  const apiErr = error instanceof ApiError ? error : null
  const notFound = apiErr?.code === 'NOT_FOUND'
  const notDir = apiErr?.code === 'VALIDATION_FAILED' && apiErr.details.reason === 'not_a_directory'

  let body: React.ReactNode
  if (isPending) {
    // SPEC §9.7: the skeleton fades in only after 300 ms (CSS delay) so short loads do not flicker.
    body = (
      <div className="delayed-reveal space-y-2 p-3" aria-busy="true" data-testid="details-skeleton">
        {Array.from({ length: 12 }).map((_, i) => (
          <Skeleton key={i} className="h-7 w-full" />
        ))}
      </div>
    )
  } else if (isError) {
    body = (
      <EmptyState
        icon={FolderOpen}
        title={
          notFound
            ? t('files:error.notFound')
            : notDir
              ? t('files:error.notDirectory')
              : t('files:error.title')
        }
        description={apiErr ? t('files:error.code', { code: apiErr.code }) : (error?.message ?? '')}
        className="h-full"
        action={
          <>
            {!notFound && !notDir ? (
              <Button variant="outline" onClick={() => void refetch()}>
                {t('files:error.retry')}
              </Button>
            ) : null}
            {parent !== null ? (
              <Button variant="outline" onClick={() => go(parent)}>
                {t('files:error.goParent')}
              </Button>
            ) : null}
            <Button variant="primary" onClick={() => go(ROOT)}>
              {t('files:error.goHome')}
            </Button>
          </>
        }
      />
    )
  } else if (items.length === 0) {
    body = (
      <EmptyState
        icon={FolderOpen}
        title={t('files:empty.title')}
        description={t('files:empty.description')}
        className="h-full"
        action={
          <>
            <Button variant="outline" disabled>
              <Upload aria-hidden="true" />
              {t('files:empty.upload')}
            </Button>
            <Button variant="primary" onClick={() => setNewKind('folder')}>
              <FolderPlus aria-hidden="true" />
              {t('files:empty.newFolder')}
            </Button>
          </>
        }
      />
    )
  } else {
    body = (
      <DetailsView
        items={items}
        total={total}
        path={path}
        hasNextPage={listing.hasNextPage}
        isFetchingNextPage={listing.isFetchingNextPage}
        fetchNextPage={() => void listing.fetchNextPage()}
        onOpen={onOpen}
        columns={isMobile ? MOBILE_COLUMNS : DEFAULT_COLUMNS}
        initialScroll={tab.scroll[path] ?? 0}
        onScrollChange={(off) => useTabsStore.getState().setScroll(path, off)}
      />
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="browser">
      <AddressBar
        path={path}
        canBack={tabs.canBack()}
        canForward={tabs.canForward()}
        canUp={parent !== null}
        onBack={handlers.back}
        onForward={handlers.forward}
        onUp={handlers.up}
        onRefresh={handlers.refresh}
        onNavigate={go}
        editing={editing}
        onEditingChange={setEditing}
        compact={isMobile}
        refreshing={isFetching && !isPending}
      />
      <Toolbar
        onNewFolder={() => setNewKind('folder')}
        onNewFile={() => setNewKind('file')}
        compact={isMobile}
      />
      <div className="flex min-h-0 flex-1 flex-col">{body}</div>
      <StatusBar total={total} />
      <NewItemDialog
        kind={newKind}
        dir={path}
        onClose={() => {
          setNewKind(null)
          void qc.invalidateQueries({ queryKey: ['list', path] })
        }}
      />
    </div>
  )
}
