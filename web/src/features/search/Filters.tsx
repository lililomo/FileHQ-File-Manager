import { useTranslation } from 'react-i18next'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { TagDot } from '@/features/browser/Badges'
import { TAG_COLORS } from '@/features/meta/api'
import type { SearchKind } from './api'
import { DEFAULT_FILTERS, TYPE_OPTIONS, type FilterState } from './filters'

/** Filter panel below the query (SPEC §8.12): shared fields plus the
 *  mode flags that differ between name (glob/regex) and content
 *  (regex/case/whole word) search. */
export function Filters({
  kind,
  value,
  onChange,
}: {
  kind: SearchKind
  value: FilterState
  onChange: (next: FilterState) => void
}) {
  const { t } = useTranslation(['search', 'files'])
  const lt = t as unknown as (key: string) => string
  const set = <K extends keyof FilterState>(k: K, v: FilterState[K]) =>
    onChange({ ...value, [k]: v })
  const check = (id: keyof FilterState, label: string, testId: string) => (
    <label className="flex items-center gap-2 text-sm" htmlFor={`sf-${id}`}>
      <Checkbox
        id={`sf-${id}`}
        checked={value[id] as boolean}
        onCheckedChange={(c) => set(id, c === true)}
        data-testid={testId}
      />
      {label}
    </label>
  )
  return (
    <div
      className="grid gap-3 border-b border-border bg-surface-2 px-4 py-3 sm:grid-cols-2 lg:grid-cols-4"
      data-testid="search-filters"
    >
      <div className="flex flex-wrap items-center gap-4 sm:col-span-2 lg:col-span-4">
        {check('recursive', t('search:scope.recursive'), 'filter-recursive')}
        {kind === 'name' ? (
          <>
            {check('glob', t('search:filters.glob'), 'filter-glob')}
            {check('regex', t('search:filters.regex'), 'filter-regex')}
          </>
        ) : (
          <>
            {check('regex', t('search:filters.regex'), 'filter-regex')}
            {check('caseSensitive', t('search:filters.caseSensitive'), 'filter-case')}
            {check('wholeWord', t('search:filters.wholeWord'), 'filter-word')}
          </>
        )}
        <Button
          size="xs"
          variant="ghost"
          className="ml-auto"
          onClick={() => onChange({ ...DEFAULT_FILTERS })}
          data-testid="filter-reset"
        >
          {t('search:filters.reset')}
        </Button>
      </div>
      {kind === 'name' ? (
        <div className="grid gap-1">
          <Label htmlFor="sf-type">{t('search:filters.types')}</Label>
          <Select
            value={value.type || 'any'}
            onValueChange={(v) => set('type', v === 'any' ? '' : v)}
          >
            <SelectTrigger id="sf-type" className="h-8" data-testid="filter-type">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">{t('search:filters.anyType')}</SelectItem>
              {TYPE_OPTIONS.map((c) => (
                <SelectItem key={c} value={c}>
                  {t(`search:types.${c}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      ) : null}
      <div className="grid gap-1">
        <Label htmlFor="sf-ext">{t('search:filters.ext')}</Label>
        <Input
          id="sf-ext"
          className="h-8"
          value={value.ext}
          placeholder={t('search:filters.extPlaceholder')}
          onChange={(e) => set('ext', e.target.value)}
          data-testid="filter-ext"
        />
      </div>
      <div className="grid gap-1">
        <Label htmlFor="sf-min">{t('search:filters.minSize')}</Label>
        <Input
          id="sf-min"
          className="h-8"
          type="number"
          min={0}
          value={value.minKB}
          onChange={(e) => set('minKB', e.target.value)}
          data-testid="filter-min"
        />
      </div>
      <div className="grid gap-1">
        <Label htmlFor="sf-max">{t('search:filters.maxSize')}</Label>
        <Input
          id="sf-max"
          className="h-8"
          type="number"
          min={0}
          value={value.maxKB}
          onChange={(e) => set('maxKB', e.target.value)}
          data-testid="filter-max"
        />
      </div>
      <div className="grid gap-1">
        <Label htmlFor="sf-from">{t('search:filters.from')}</Label>
        <Input
          id="sf-from"
          className="h-8"
          type="date"
          value={value.from}
          onChange={(e) => set('from', e.target.value)}
          data-testid="filter-from"
        />
      </div>
      <div className="grid gap-1">
        <Label htmlFor="sf-to">{t('search:filters.to')}</Label>
        <Input
          id="sf-to"
          className="h-8"
          type="date"
          value={value.to}
          onChange={(e) => set('to', e.target.value)}
          data-testid="filter-to"
        />
      </div>
      {kind === 'name' ? (
        <div className="grid gap-1">
          <Label htmlFor="sf-tag">{t('search:filters.tag')}</Label>
          <Select
            value={value.tag || 'any'}
            onValueChange={(v) => set('tag', v === 'any' ? '' : v)}
          >
            <SelectTrigger id="sf-tag" className="h-8" data-testid="filter-tag">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">{t('search:filters.anyTag')}</SelectItem>
              {TAG_COLORS.map((c) => (
                <SelectItem key={c} value={c}>
                  <span className="inline-flex items-center gap-2">
                    <TagDot color={c} />
                    {lt(`files:tag.colors.${c}`)}
                  </span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      ) : null}
    </div>
  )
}
