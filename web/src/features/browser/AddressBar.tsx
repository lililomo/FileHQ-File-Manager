import { ArrowLeft, ArrowRight, ArrowUp, RotateCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { Breadcrumb } from './Breadcrumb'
import { normalizeTypedPath } from './path'

export interface AddressBarProps {
  path: string
  canBack: boolean
  canForward: boolean
  canUp: boolean
  onBack: () => void
  onForward: () => void
  onUp: () => void
  onRefresh: () => void
  onNavigate: (path: string) => void
  /** Controlled typing mode (Alt+D from the layout). */
  editing: boolean
  onEditingChange: (editing: boolean) => void
  compact?: boolean
  refreshing?: boolean
}

export function AddressBar({
  path,
  canBack,
  canForward,
  canUp,
  onBack,
  onForward,
  onUp,
  onRefresh,
  onNavigate,
  editing,
  onEditingChange,
  compact = false,
  refreshing = false,
}: AddressBarProps) {
  const { t } = useTranslation('files')

  return (
    <div
      className="flex h-10 shrink-0 items-center gap-1 border-b border-border bg-surface px-2"
      data-testid="address-bar"
    >
      {!compact ? (
        <>
          <IconButton
            label={t('nav.back')}
            shortcut="Alt+←"
            onClick={onBack}
            disabled={!canBack}
            data-testid="nav-back"
          >
            <ArrowLeft aria-hidden="true" />
          </IconButton>
          <IconButton
            label={t('nav.forward')}
            shortcut="Alt+→"
            onClick={onForward}
            disabled={!canForward}
            data-testid="nav-forward"
          >
            <ArrowRight aria-hidden="true" />
          </IconButton>
        </>
      ) : null}
      <IconButton
        label={t('nav.up')}
        shortcut="Alt+↑"
        onClick={onUp}
        disabled={!canUp}
        data-testid="nav-up"
      >
        <ArrowUp aria-hidden="true" />
      </IconButton>
      <IconButton
        label={t('nav.refresh')}
        shortcut="F5"
        onClick={onRefresh}
        data-testid="nav-refresh"
        className={cn(refreshing && '[&_svg]:animate-spin')}
      >
        <RotateCw aria-hidden="true" />
      </IconButton>
      <div className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
      {editing ? (
        <PathInput
          key={path}
          path={path}
          onCancel={() => onEditingChange(false)}
          onSubmit={(next) => {
            onEditingChange(false)
            if (next !== path) onNavigate(next)
          }}
        />
      ) : (
        <Breadcrumb path={path} compact={compact} onEditRequest={() => onEditingChange(true)} />
      )}
    </div>
  )
}

function PathInput({
  path,
  onSubmit,
  onCancel,
}: {
  path: string
  onSubmit: (path: string) => void
  onCancel: () => void
}) {
  const { t } = useTranslation('files')
  const [value, setValue] = useState(path)
  const [invalid, setInvalid] = useState(false)
  return (
    <form
      className="flex min-w-0 flex-1 items-center"
      onSubmit={(e) => {
        e.preventDefault()
        const res = normalizeTypedPath(value, path)
        if (!res.ok) {
          setInvalid(true)
          return
        }
        onSubmit(res.path)
      }}
    >
      <Input
        ref={(el) => {
          if (el && document.activeElement !== el) {
            el.focus()
            el.select()
          }
        }}
        value={value}
        onChange={(e) => {
          setValue(e.target.value)
          setInvalid(false)
        }}
        onBlur={onCancel}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onCancel()
          }
        }}
        aria-label={t('nav.pathLabel')}
        aria-invalid={invalid || undefined}
        placeholder={t('nav.pathPlaceholder')}
        spellCheck={false}
        autoComplete="off"
        className="h-7 font-mono text-sm"
        data-testid="path-input"
      />
      {invalid ? (
        <span role="alert" className="ml-2 shrink-0 text-xs text-danger">
          {t('nav.pathInvalid')}
        </span>
      ) : null}
    </form>
  )
}
