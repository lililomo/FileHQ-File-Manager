import { useNavigate } from '@tanstack/react-router'
import { Folder, Search } from 'lucide-react'
import { useMemo, useState, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from '@/components/ui/command'
import { pathToSplat, ROOT } from '@/features/browser/path'
import {
  applicableActions,
  getActionContext,
  subscribeActionContext,
  type ActionDef,
} from '@/lib/actions'
import { formatKeys, shortcutById } from '@/lib/shortcuts'
import { useOverlaysStore } from '@/stores/overlays'
import { useTabsStore } from '@/stores/tabs'

/** Ctrl+K command palette (PROMPT 7b task 9): jump to folders from the tab
 *  histories, run registry actions, and (Step 15) search files. */
export function CommandPalette() {
  const { t } = useTranslation(['files', 'nav'])
  const open = useOverlaysStore((s) => s.paletteOpen)
  const setOpen = useOverlaysStore((s) => s.setPalette)
  const navigate = useNavigate()
  const tabs = useTabsStore((s) => s.tabs)
  const [query, setQuery] = useState('')
  const ctx = useSyncExternalStore(subscribeActionContext, getActionContext, getActionContext)

  const folders = useMemo(() => {
    const seen = new Set<string>([ROOT])
    const out: string[] = [ROOT]
    for (const tab of tabs) {
      for (const p of [...tab.history].reverse()) {
        if (!seen.has(p)) {
          seen.add(p)
          out.push(p)
        }
      }
    }
    return out.slice(0, 12)
  }, [tabs])
  const actions = ctx ? applicableActions(ctx) : []

  const close = (o = false) => {
    setOpen(o)
    if (!o) setQuery('')
  }
  const go = (path: string) => {
    close()
    void navigate({ to: '/files/$', params: { _splat: pathToSplat(path) } })
  }
  const run = (a: ActionDef) => {
    close()
    if (ctx) a.run(ctx)
  }
  const shortcutKeys = (a: ActionDef) => {
    const def = a.shortcut ? shortcutById(a.shortcut) : undefined
    return def ? formatKeys(def.keys).join('+') : null
  }

  return (
    <CommandDialog
      open={open}
      onOpenChange={close}
      title={t('files:palette.title')}
      description={t('files:palette.description')}
      className="sm:max-w-lg"
    >
      <CommandInput
        placeholder={t('files:palette.placeholder')}
        value={query}
        onValueChange={setQuery}
        data-testid="palette-input"
      />
      <CommandList>
        <CommandEmpty>{t('files:palette.empty')}</CommandEmpty>
        <CommandGroup heading={t('files:palette.jump')}>
          {folders.map((p) => (
            <CommandItem key={p} value={`jump ${p}`} onSelect={() => go(p)}>
              <Folder className="text-icon-folder" aria-hidden="true" />
              <span className="truncate font-mono text-xs">{p === ROOT ? t('nav:files') : p}</span>
            </CommandItem>
          ))}
        </CommandGroup>
        {actions.length > 0 ? (
          <>
            <CommandSeparator />
            <CommandGroup heading={t('files:palette.actions')}>
              {actions.map((a) => {
                const Icon = a.icon
                const keys = shortcutKeys(a)
                return (
                  <CommandItem
                    key={a.id}
                    value={`action ${a.id} ${t(`files:actions.${a.labelKey}` as 'files:actions.refresh')}`}
                    onSelect={() => run(a)}
                    data-testid={`palette-action-${a.id}`}
                  >
                    <Icon aria-hidden="true" />
                    <span>{t(`files:actions.${a.labelKey}` as 'files:actions.refresh')}</span>
                    {keys ? <CommandShortcut>{keys}</CommandShortcut> : null}
                  </CommandItem>
                )
              })}
            </CommandGroup>
          </>
        ) : null}
        <CommandSeparator />
        <CommandGroup heading={t('files:palette.search')}>
          <CommandItem
            value={`search files ${query}`}
            onSelect={() => {
              const q = query.trim()
              close()
              void navigate({ to: '/search', search: q ? { q } : {} })
            }}
            data-testid="palette-search-files"
          >
            <Search aria-hidden="true" />
            <span>
              {query.trim()
                ? t('files:palette.searchFor', { q: query.trim() })
                : t('files:palette.searchFiles')}
            </span>
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}
