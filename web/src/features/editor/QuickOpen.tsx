import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { FileIcon } from '@/components/file-icon/FileIcon'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { nameQueryKey, searchNames } from '@/features/search/api'

/** Ctrl+P quick open: name search over the whole home (Step 15 API), files only. */
export function QuickOpen({
  open,
  onOpenChange,
  onPick,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  onPick: (path: string) => void
}) {
  const { t } = useTranslation('editor')
  const [q, setQ] = useState('')
  const [debounced, setDebounced] = useState('')
  useEffect(() => {
    const id = setTimeout(() => setDebounced(q.trim()), 150)
    return () => clearTimeout(id)
  }, [q])
  const filters = { recursive: true, types: ['code', 'text', 'document', 'other'] }
  const res = useQuery({
    queryKey: [...nameQueryKey(debounced, '/', filters), 'quick-open'],
    queryFn: ({ signal }) => searchNames(debounced, '/', filters, undefined, signal),
    enabled: open && debounced.length > 0,
    staleTime: 10_000,
  })
  const items = (res.data?.items ?? []).filter((it) => !it.isDir).slice(0, 50)
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) setQ('')
        onOpenChange(o)
      }}
    >
      <DialogContent
        className="top-[15%] translate-y-0 p-0 sm:max-w-lg"
        showCloseButton={false}
        data-testid="quick-open"
      >
        <DialogTitle className="sr-only">{t('toolbar.quickOpen')}</DialogTitle>
        <Command shouldFilter={false}>
          <CommandInput
            value={q}
            onValueChange={setQ}
            placeholder={t('quickOpen.placeholder')}
            autoFocus
          />
          <CommandList>
            {debounced && !res.isFetching && items.length === 0 ? (
              <CommandEmpty>{t('quickOpen.empty')}</CommandEmpty>
            ) : null}
            <CommandGroup heading={t('quickOpen.hint')}>
              {items.map((it) => (
                <CommandItem
                  key={it.path}
                  value={it.path}
                  onSelect={() => {
                    onPick(it.path)
                    onOpenChange(false)
                  }}
                >
                  <FileIcon category={it.category} size={16} />
                  <span className="truncate">{it.name}</span>
                  <span className="ml-auto truncate text-xs text-text-muted">{it.location}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  )
}
