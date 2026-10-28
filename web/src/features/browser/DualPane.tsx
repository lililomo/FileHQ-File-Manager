import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { setMiniPlayerSlot } from '@/features/viewer/audio/miniPlayerSlot'
import { announce } from '@/lib/announce'
import { registerShortcut } from '@/lib/shortcuts'
import { useIsMobile, useMediaQuery } from '@/lib/useMediaQuery'
import { cn } from '@/lib/utils'
import {
  DUAL_PANE_QUERY,
  hydrateRightPane,
  isDualPaneActive,
  otherPane,
  rightPrefOf,
  usePanesStore,
  useRightTabsStore,
  type PaneId,
} from '@/stores/panes'
import { getPref, usePref, usePrefsStore } from '@/stores/prefs'
import { useSessionStore } from '@/stores/session'
import { BrowserLayout } from './BrowserLayout'

export interface FilesBrowserProps {
  path: string
  selectName?: string | undefined
  archivePath?: string | undefined
  viewPath?: string | undefined
}

/** `/files/*` screen: one pane, or two side by side (SPEC §9.3 "varian
 *  dual-pane", Step 23b). The left pane is always rendered at the same tree
 *  position so toggling dual-pane does not remount it; the right pane and the
 *  shared slots (toolbar, preview, mini-player) only exist in dual-pane. */
export function FilesBrowser({ path, selectName, archivePath, viewPath }: FilesBrowserProps) {
  const { t } = useTranslation('files')
  const wide = useMediaQuery(DUAL_PANE_QUERY)
  const mobile = useIsMobile()
  const enabled = usePref<boolean>('dualpane.enabled', false)
  const prefsLoaded = usePrefsStore((s) => s.loaded)
  const userId = useSessionStore((s) => s.user?.id ?? null)
  const dual = isDualPaneActive(enabled, wide, mobile)
  const focused = usePanesStore((s) => s.focused)
  const rightPath = useRightTabsStore(
    (s) => (s.tabs.find((x) => x.id === s.activeId) ?? s.tabs[0]!).path,
  )
  const [toolbarSlot, setToolbarSlot] = useState<HTMLElement | null>(null)
  const [previewSlot, setPreviewSlot] = useState<HTMLElement | null>(null)
  const rowRef = useRef<HTMLDivElement>(null)

  const setEnabled = useCallback(
    (v: boolean) => {
      usePrefsStore.getState().set('dualpane.enabled', v)
      if (v) usePanesStore.getState().focus('left')
      announce(t(v ? 'dualPane.on' : 'dualPane.off'))
    },
    [t],
  )
  // the toggle exists on wide screens only (below 1280 px the pref stays stored)
  const toggleAvailable = wide && !mobile
  // stable object: it is a dependency of each pane's action context
  const dualToggle = useMemo(
    () => (toggleAvailable ? { enabled, onChange: setEnabled } : undefined),
    [toggleAvailable, enabled, setEnabled],
  )
  useEffect(() => {
    if (!toggleAvailable) return
    return registerShortcut('dualPane', () => setEnabled(!getPref('dualpane.enabled', false)))
  }, [toggleAvailable, setEnabled])

  // right pane: restore `dualpane.right` once per user, then persist its tabs on change
  useEffect(() => {
    if (!prefsLoaded || !userId) return
    hydrateRightPane(getPref<unknown>('dualpane.right', undefined), userId)
    return useRightTabsStore.subscribe((s) =>
      usePrefsStore.getState().set('dualpane.right', rightPrefOf(s)),
    )
  }, [prefsLoaded, userId])

  // announce which pane took the focus
  useEffect(() => {
    if (!dual) return
    return usePanesStore.subscribe((s, prev) => {
      if (s.focused !== prev.focused)
        announce(t('dualPane.focused', { pane: t(`dualPane.${s.focused}`) }))
    })
  }, [dual, t])

  // Commander-style Tab: from a pane's grid straight to the other pane's grid;
  // Shift+Tab keeps the normal order (address bar, tabs of the pane)
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!dual || e.key !== 'Tab' || e.shiftKey || e.altKey || e.ctrlKey || e.metaKey) return
    const target = e.target instanceof HTMLElement ? e.target : null
    if (target?.getAttribute('role') !== 'grid') return
    const from = target.closest<HTMLElement>('[data-pane-host]')?.dataset.paneHost as
      PaneId | undefined
    if (!from) return
    const grid = rowRef.current?.querySelector<HTMLElement>(
      `[data-pane-host="${otherPane(from)}"] [role="grid"]`,
    )
    if (!grid) return
    e.preventDefault()
    grid.focus()
  }

  return (
    <div className="flex h-full min-h-0 flex-col" data-dual-pane={dual || undefined}>
      {dual ? (
        <div ref={setToolbarSlot} className="shrink-0" data-testid="dual-pane-toolbar" />
      ) : null}
      <div ref={rowRef} className="flex min-h-0 flex-1" onKeyDown={onKeyDown}>
        <PaneHost pane="left" dual={dual} focused={focused === 'left'} label={t('dualPane.left')}>
          <BrowserLayout
            path={path}
            selectName={selectName}
            archivePath={archivePath}
            viewPath={viewPath}
            dualToggle={dualToggle}
            dual={
              dual
                ? {
                    pane: 'left',
                    focused: focused === 'left',
                    toolbarSlot,
                    previewSlot,
                    otherPath: rightPath,
                  }
                : undefined
            }
          />
        </PaneHost>
        {dual ? (
          <PaneHost
            pane="right"
            dual
            focused={focused === 'right'}
            label={t('dualPane.right')}
            className="border-l border-border"
          >
            <BrowserLayout
              path={rightPath}
              dualToggle={dualToggle}
              dual={{
                pane: 'right',
                focused: focused === 'right',
                toolbarSlot,
                previewSlot,
                otherPath: path,
              }}
            />
          </PaneHost>
        ) : null}
        {dual ? <div ref={setPreviewSlot} className="relative flex shrink-0 empty:hidden" /> : null}
      </div>
      {dual ? (
        <div
          ref={setMiniPlayerSlot}
          className="shrink-0 empty:hidden"
          data-testid="mini-player-slot"
        />
      ) : null}
    </div>
  )
}

/** One pane: a labelled region whose pointer/keyboard focus makes it the
 *  focused pane. Events from portals (dialogs, the shared toolbar) bubble
 *  through React but are not inside the pane's DOM and are ignored. */
function PaneHost({
  pane,
  dual,
  focused,
  label,
  className,
  children,
}: {
  pane: PaneId
  dual: boolean
  focused: boolean
  label: string
  className?: string
  children: ReactNode
}) {
  const take = (e: React.SyntheticEvent) => {
    if (!dual || !(e.target instanceof Node) || !e.currentTarget.contains(e.target)) return
    usePanesStore.getState().focus(pane)
  }
  return (
    <div
      role={dual ? 'region' : undefined}
      aria-label={dual ? label : undefined}
      data-pane-host={pane}
      data-pane-focused={dual && focused ? 'true' : undefined}
      onPointerDownCapture={take}
      onFocusCapture={take}
      className={cn(
        'relative flex min-h-0 min-w-0 flex-1 flex-col [&>[data-testid=browser]]:min-h-0 [&>[data-testid=browser]]:flex-1',
        dual && focused && 'outline-2 -outline-offset-2 outline-focus outline-solid',
        className,
      )}
    >
      {children}
    </div>
  )
}
