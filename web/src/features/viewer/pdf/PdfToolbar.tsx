import {
  ChevronLeft,
  ChevronRight,
  Download,
  Loader2,
  Maximize2,
  PanelLeft,
  Printer,
  RotateCw,
  Search,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

/** Zoom presets of the select (pdf.js scale values). */
const ZOOM_PRESETS = [
  'auto',
  'page-fit',
  'page-width',
  '0.5',
  '0.75',
  '1',
  '1.25',
  '1.5',
  '2',
  '3',
  '4',
] as const

export interface PdfToolbarProps {
  page: number
  pageCount: number
  onPage: (n: number) => void
  scalePercent: number
  scaleValue: string
  onScaleValue: (v: string) => void
  onZoomIn: () => void
  onZoomOut: () => void
  onRotate: () => void
  sidebarOpen: boolean
  onToggleSidebar: () => void
  searchOpen: boolean
  onToggleSearch: () => void
  onPrint: () => void
  printing: number | null
  onDownload: () => void
  onFullscreen: () => void
}

export function PdfToolbar(p: PdfToolbarProps) {
  const { t } = useTranslation('viewer')
  const [draft, setDraft] = useState(String(p.page))
  // the page changed underneath (scroll/thumbnail): reset the draft during render
  const [seenPage, setSeenPage] = useState(p.page)
  if (seenPage !== p.page) {
    setSeenPage(p.page)
    setDraft(String(p.page))
  }
  const commit = () => {
    const n = Number.parseInt(draft, 10)
    if (Number.isFinite(n) && n >= 1 && n <= p.pageCount) p.onPage(n)
    else setDraft(String(p.page))
  }
  const presetLabel = (v: string) => {
    switch (v) {
      case 'auto':
        return t('pdf.zoomAuto')
      case 'page-fit':
        return t('pdf.fitPage')
      case 'page-width':
        return t('pdf.fitWidth')
      default:
        return `${Math.round(Number(v) * 100)}%`
    }
  }
  const isPreset = (ZOOM_PRESETS as readonly string[]).includes(p.scaleValue)
  return (
    <div
      className="flex h-10 shrink-0 items-center gap-0.5 border-b border-border bg-surface px-2"
      role="toolbar"
      aria-label={t('pdf.toolbar')}
    >
      <IconButton
        label={t('pdf.sidebar')}
        onClick={p.onToggleSidebar}
        aria-pressed={p.sidebarOpen}
        className={p.sidebarOpen ? 'bg-surface-2 text-text' : undefined}
        data-testid="pdf-sidebar-toggle"
      >
        <PanelLeft aria-hidden="true" />
      </IconButton>
      <IconButton
        label={t('pdf.search')}
        shortcut="Ctrl+F"
        onClick={p.onToggleSearch}
        aria-pressed={p.searchOpen}
        className={p.searchOpen ? 'bg-surface-2 text-text' : undefined}
        data-testid="pdf-search-toggle"
      >
        <Search aria-hidden="true" />
      </IconButton>
      <span className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
      <IconButton
        label={t('pdf.prevPage')}
        size="icon-sm"
        disabled={p.page <= 1}
        onClick={() => p.onPage(p.page - 1)}
        data-testid="pdf-prev-page"
      >
        <ChevronLeft aria-hidden="true" />
      </IconButton>
      <Input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            commit()
          }
        }}
        inputMode="numeric"
        aria-label={t('pdf.pageInput')}
        className="h-7 w-14 text-center text-sm tabular"
        data-testid="pdf-page-input"
      />
      <span className="text-xs text-text-muted tabular" data-testid="pdf-page-count">
        {t('pdf.of', { n: p.pageCount })}
      </span>
      <IconButton
        label={t('pdf.nextPage')}
        size="icon-sm"
        disabled={p.page >= p.pageCount}
        onClick={() => p.onPage(p.page + 1)}
        data-testid="pdf-next-page"
      >
        <ChevronRight aria-hidden="true" />
      </IconButton>
      <span className="mx-1 h-5 w-px bg-border" aria-hidden="true" />
      <IconButton label={t('pdf.zoomOut')} shortcut="-" onClick={p.onZoomOut}>
        <ZoomOut aria-hidden="true" />
      </IconButton>
      <Select value={isPreset ? p.scaleValue : 'custom'} onValueChange={p.onScaleValue}>
        <SelectTrigger
          size="sm"
          className="h-7 w-[132px] text-xs"
          aria-label={t('pdf.zoom')}
          data-testid="pdf-zoom"
        >
          <SelectValue>{isPreset ? presetLabel(p.scaleValue) : `${p.scalePercent}%`}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {ZOOM_PRESETS.map((v) => (
            <SelectItem key={v} value={v}>
              {presetLabel(v)}
            </SelectItem>
          ))}
          {!isPreset ? (
            <SelectItem value="custom" disabled>
              {p.scalePercent}%
            </SelectItem>
          ) : null}
        </SelectContent>
      </Select>
      <IconButton label={t('pdf.zoomIn')} shortcut="+" onClick={p.onZoomIn}>
        <ZoomIn aria-hidden="true" />
      </IconButton>
      <IconButton
        label={t('pdf.rotate')}
        shortcut="R"
        onClick={p.onRotate}
        data-testid="pdf-rotate"
      >
        <RotateCw aria-hidden="true" />
      </IconButton>
      <span className="flex-1" />
      {p.printing !== null ? (
        <span className="flex items-center gap-1 text-xs text-text-muted" aria-live="polite">
          <Loader2 className="size-3 animate-spin" aria-hidden="true" />
          {t('pdf.printPreparing', { n: p.printing })}
        </span>
      ) : null}
      <IconButton
        label={t('pdf.print')}
        onClick={p.onPrint}
        disabled={p.printing !== null}
        data-testid="pdf-print"
      >
        <Printer aria-hidden="true" />
      </IconButton>
      <IconButton label={t('pdf.download')} onClick={p.onDownload}>
        <Download aria-hidden="true" />
      </IconButton>
      <IconButton label={t('image.fullscreen')} shortcut="F" onClick={p.onFullscreen}>
        <Maximize2 aria-hidden="true" />
      </IconButton>
    </div>
  )
}
