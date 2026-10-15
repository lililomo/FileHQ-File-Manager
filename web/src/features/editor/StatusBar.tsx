import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { EditorTab } from '@/stores/editor'
import { ENCODINGS } from './api'
import { useWorkspace } from './controller'
import { LANGUAGE_OPTIONS, languageLabel } from './languages'
import { getModel } from './models'

/** Status bar (SPEC §8.13): Ln/Col, encoding (reopen / save with), EOL,
 *  indentation, language, size, location, history placeholder, read-only. */
export function StatusBar({ tab }: { tab: EditorTab | null }) {
  const { t, i18n } = useTranslation('editor')
  const ws = useWorkspace()
  const [indentState, setIndentState] = useState(0)
  if (!tab)
    return (
      <div
        className="h-7 shrink-0 border-t border-border bg-surface-2"
        data-testid="editor-status"
      />
    )
  const model = tab.status === 'ready' ? getModel(tab.path) : undefined
  const opts = model?.getOptions()
  const insertSpaces = opts?.insertSpaces ?? true
  const tabSize = opts?.tabSize ?? 4
  void indentState
  const chip =
    'rounded px-1.5 py-0.5 hover:bg-surface-3 focus-visible:ring-2 focus-visible:ring-focus outline-none'
  return (
    <div
      data-testid="editor-status"
      className="flex h-7 shrink-0 items-center gap-1 overflow-hidden border-t border-border bg-surface-2 px-2 text-xs text-text-muted"
    >
      <span className="tabular-nums" data-testid="status-position">
        {t('status.position', { line: tab.cursor.line, col: tab.cursor.col })}
      </span>
      {tab.readOnly ? (
        <span className="rounded bg-warning/20 px-1.5 text-text" data-testid="status-readonly">
          {t('status.readOnly')}
        </span>
      ) : null}
      {tab.saving ? <span>{t('status.saving')}</span> : null}
      <span
        className="min-w-0 flex-1 truncate text-right"
        title={tab.path}
        data-testid="status-location"
      >
        {tab.path}
      </span>
      <span className="tabular-nums">{formatBytes(tab.size, i18n.resolvedLanguage ?? 'id')}</span>
      {/* indentation */}
      <DropdownMenu>
        <DropdownMenuTrigger className={chip} aria-label={t('status.indent')} disabled={!model}>
          {insertSpaces ? t('status.spaces', { n: tabSize }) : t('status.tabs', { n: tabSize })}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuCheckboxItem
            checked={insertSpaces}
            onCheckedChange={() => {
              ws.setIndent(tab.path, { insertSpaces: true })
              setIndentState((n) => n + 1)
            }}
          >
            {t('indent.useSpaces')}
          </DropdownMenuCheckboxItem>
          <DropdownMenuCheckboxItem
            checked={!insertSpaces}
            onCheckedChange={() => {
              ws.setIndent(tab.path, { insertSpaces: false })
              setIndentState((n) => n + 1)
            }}
          >
            {t('indent.useTabs')}
          </DropdownMenuCheckboxItem>
          <DropdownMenuSeparator />
          <DropdownMenuLabel>{t('indent.size')}</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={String(tabSize)}
            onValueChange={(v) => {
              ws.setIndent(tab.path, { tabSize: Number(v) })
              setIndentState((n) => n + 1)
            }}
          >
            {[2, 4, 8].map((n) => (
              <DropdownMenuRadioItem key={n} value={String(n)}>
                {n}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {/* encoding */}
      <DropdownMenu>
        <DropdownMenuTrigger
          className={chip}
          aria-label={t('status.encoding')}
          data-testid="status-encoding"
          disabled={tab.status !== 'ready'}
        >
          {tab.encoding.toUpperCase()}
          {tab.bom ? ` ${t('status.bom')}` : ''}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="max-h-80 overflow-y-auto">
          <DropdownMenuLabel>
            {t('encoding.detected', { enc: tab.detectedEncoding.toUpperCase() })}
          </DropdownMenuLabel>
          <DropdownMenuSub>
            <DropdownMenuSubTrigger data-testid="encoding-reopen">
              {t('encoding.reopen')}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="max-h-80 overflow-y-auto">
              {ENCODINGS.map((enc) => (
                <DropdownMenuItem
                  key={enc}
                  onSelect={() => void ws.reopenWithEncoding(tab.path, enc)}
                  data-testid={`reopen-${enc}`}
                >
                  {enc.toUpperCase()}
                </DropdownMenuItem>
              ))}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
          <DropdownMenuSub>
            <DropdownMenuSubTrigger disabled={tab.readOnly} data-testid="encoding-save-with">
              {t('encoding.saveWith')}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="max-h-80 overflow-y-auto">
              {ENCODINGS.map((enc) => (
                <DropdownMenuItem
                  key={enc}
                  onSelect={() => void ws.saveWithEncoding(tab.path, enc, enc.startsWith('utf-16'))}
                  data-testid={`save-with-${enc}`}
                >
                  {enc.toUpperCase()}
                </DropdownMenuItem>
              ))}
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => void ws.saveWithEncoding(tab.path, 'utf-8', true)}>
                {t('encoding.withBom')}
              </DropdownMenuItem>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        </DropdownMenuContent>
      </DropdownMenu>
      {/* EOL */}
      <DropdownMenu>
        <DropdownMenuTrigger
          className={chip}
          aria-label={t('eol.change')}
          data-testid="status-eol"
          disabled={tab.status !== 'ready' || tab.readOnly}
        >
          {t(`eol.${tab.eol}`)}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuRadioGroup
            value={tab.eol}
            onValueChange={(v) => ws.setEOL(tab.path, v as 'lf' | 'crlf')}
          >
            <DropdownMenuRadioItem value="lf" data-testid="eol-lf">
              {t('eol.lf')}
            </DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="crlf" data-testid="eol-crlf">
              {t('eol.crlf')}
            </DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {/* language */}
      <DropdownMenu>
        <DropdownMenuTrigger
          className={chip}
          aria-label={t('status.language')}
          data-testid="status-language"
          disabled={tab.status !== 'ready'}
        >
          {languageLabel(tab.language)}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="max-h-80 overflow-y-auto">
          <DropdownMenuRadioGroup
            value={tab.language}
            onValueChange={(v) => ws.setLanguage(tab.path, v)}
          >
            {LANGUAGE_OPTIONS.map((l) => (
              <DropdownMenuRadioItem key={l.id} value={l.id}>
                {l.label}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      <span
        className={cn(chip, 'cursor-default opacity-60')}
        title={t('status.historySoon')}
        data-testid="status-history"
      >
        {t('status.history', { n: 0 })}
      </span>
    </div>
  )
}
