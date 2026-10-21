import { useQuery } from '@tanstack/react-query'
import { Copy, FolderOpen, X } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import type { Item } from '@/features/browser/api'
import { typeLabel } from '@/features/browser/columns'
import { formatBytes, formatDateTimeFull, formatDuration } from '@/lib/format'
import { usePref } from '@/stores/prefs'
import { getMediaInfo, mediaInfoKey, type ExifInfo, type MediaInfo } from './api'

const DASH = '—'

export interface InfoPanelProps {
  item: Item
  onClose: () => void
  onOpenLocation?: (() => void) | undefined
}

/** Right-hand info panel of the viewer (SPEC §8.14 "panel info", PROMPT
 *  17a task 4): dimensions, size, type, dates, the EXIF subset and a GPS
 *  toggle. The position is never part of the default response: switching
 *  the toggle on refetches with `include_gps=1` (default from pref
 *  `exif.show_gps`, remembered for this session only). */
export function InfoPanel({ item, onClose, onOpenLocation }: InfoPanelProps) {
  const { t, i18n } = useTranslation(['viewer', 'files'])
  // typeLabel resolves `type.*` keys in the files namespace (review D: with
  // the viewer-default `t` the raw key "type.fallback" leaked into the panel)
  const { t: tf } = useTranslation('files')
  const lang = i18n.resolvedLanguage ?? 'id'
  const gpsPref = usePref<boolean>('exif.show_gps', false)
  const [showGps, setShowGps] = useState(gpsPref)
  const info = useQuery({
    queryKey: mediaInfoKey(item.path, showGps),
    queryFn: ({ signal }) => getMediaInfo(item.path, showGps, signal),
    staleTime: 60_000,
  })
  const mi: MediaInfo | undefined = info.data
  const width = mi?.width ?? item.media?.width ?? 0
  const height = mi?.height ?? item.media?.height ?? 0
  const exif = mi?.exif ?? null
  const isImage = mi ? mi.kind === 'image' || mi.kind === 'svg' : item.category === 'image'

  const general: [string, string][] = [
    [t('viewer:info.type'), typeLabel(tf, item)],
    [t('viewer:info.size'), formatBytes(item.size, lang)],
  ]
  if (width && height) general.push([t('viewer:info.dimensions'), `${width} × ${height}`])
  if (mi?.durationMs)
    general.push([t('viewer:info.duration'), formatDuration(mi.durationMs / 1000)])
  general.push([t('viewer:info.mime'), mi?.mime || item.mime || DASH])
  general.push([t('viewer:info.modified'), formatDateTimeFull(item.mtime, lang)])
  general.push([t('viewer:info.path'), item.path])

  const copyCoords = () => {
    if (!mi?.gps) return
    const text = `${mi.gps.lat}, ${mi.gps.lon}`
    void navigator.clipboard?.writeText(text).then(
      () => toast.success(t('viewer:info.copied')),
      () => {},
    )
  }

  return (
    <aside
      // narrow screens: the panel covers the viewer instead of squeezing it (review D)
      className="flex w-80 shrink-0 flex-col border-l border-border bg-surface max-sm:absolute max-sm:inset-0 max-sm:z-10 max-sm:w-full max-sm:border-l-0"
      aria-label={t('viewer:info.title')}
      data-testid="viewer-info"
    >
      <div className="flex h-9 items-center justify-between border-b border-border px-3 text-xs font-medium text-text-muted">
        <span>{t('viewer:info.title')}</span>
        <IconButton label={t('viewer:info.close')} shortcut="I" size="icon-sm" onClick={onClose}>
          <X aria-hidden="true" />
        </IconButton>
      </div>
      <div className="min-h-0 flex-1 space-y-4 overflow-auto p-3 text-xs">
        <section>
          <h3 className="mb-1 font-medium text-text">{t('viewer:info.general')}</h3>
          <Facts rows={general} mono={[t('viewer:info.path')]} />
          {onOpenLocation ? (
            <Button
              variant="outline"
              size="sm"
              className="mt-2 gap-1"
              onClick={onOpenLocation}
              data-testid="viewer-open-location"
            >
              <FolderOpen aria-hidden="true" />
              {t('viewer:header.openLocation')}
            </Button>
          ) : null}
        </section>
        {isImage ? (
          <section data-testid="viewer-exif">
            <h3 className="mb-1 font-medium text-text">{t('viewer:info.exif')}</h3>
            {info.isPending ? (
              <p className="text-text-muted" aria-busy="true">
                {t('viewer:info.loading')}
              </p>
            ) : info.isError ? (
              <p className="text-danger" role="alert">
                {t('viewer:info.error')}
              </p>
            ) : exif ? (
              <Facts rows={exifRows(t as unknown as LooseT, exif, lang)} />
            ) : (
              <p className="text-text-muted">{t('viewer:info.noExif')}</p>
            )}
          </section>
        ) : null}
        {isImage && exif ? (
          <section data-testid="viewer-gps">
            <h3 className="mb-1 font-medium text-text">{t('viewer:info.gps')}</h3>
            {!exif.hasGps ? (
              <p className="text-text-muted">{t('viewer:info.gpsNone')}</p>
            ) : (
              <>
                <div className="flex items-center gap-2">
                  <Switch
                    id="viewer-gps-toggle"
                    checked={showGps}
                    onCheckedChange={(v) => setShowGps(v === true)}
                    data-testid="viewer-gps-toggle"
                  />
                  <Label htmlFor="viewer-gps-toggle">{t('viewer:info.showGps')}</Label>
                </div>
                {showGps && mi?.gps ? (
                  <div className="mt-2 space-y-1" data-testid="viewer-gps-values">
                    <Facts
                      rows={[
                        [t('viewer:info.latitude'), String(mi.gps.lat)],
                        [t('viewer:info.longitude'), String(mi.gps.lon)],
                        ...(mi.gps.alt !== undefined
                          ? [[t('viewer:info.altitude'), `${mi.gps.alt} m`] as [string, string]]
                          : []),
                      ]}
                    />
                    <Button variant="ghost" size="sm" className="gap-1" onClick={copyCoords}>
                      <Copy aria-hidden="true" />
                      {t('viewer:info.copyCoords')}
                    </Button>
                  </div>
                ) : (
                  <p className="mt-1 text-text-muted" data-testid="viewer-gps-hidden">
                    {t('viewer:info.gpsHidden')}
                  </p>
                )}
              </>
            )}
          </section>
        ) : null}
      </div>
    </aside>
  )
}

function Facts({ rows, mono = [] }: { rows: [string, string][]; mono?: string[] }) {
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-text-muted">{k}</dt>
          <dd className={mono.includes(k) ? 'font-mono break-all' : 'break-words'}>{v}</dd>
        </div>
      ))}
    </dl>
  )
}

type LooseT = (key: string, opts?: Record<string, unknown>) => string

function exifRows(t: LooseT, ex: ExifInfo, lang: string): [string, string][] {
  const rows: [string, string][] = []
  // many cameras repeat the make inside the model ("Canon" + "Canon EOS R6")
  const camera =
    ex.make && ex.model && ex.model.toLowerCase().startsWith(ex.make.toLowerCase())
      ? ex.model
      : [ex.make, ex.model].filter(Boolean).join(' ')
  if (camera) rows.push([t('viewer:info.camera'), camera])
  if (ex.lens) rows.push([t('viewer:info.lens'), ex.lens])
  if (ex.dateTimeOriginal || ex.dateTime)
    rows.push([
      t('viewer:info.taken'),
      exifDateLabel(ex.dateTimeOriginal ?? ex.dateTime ?? '', lang),
    ])
  if (ex.exposureTime) rows.push([t('viewer:info.exposure'), `${ex.exposureTime} s`])
  if (ex.fNumber) rows.push([t('viewer:info.aperture'), `f/${ex.fNumber}`])
  if (ex.iso) rows.push([t('viewer:info.iso'), String(ex.iso)])
  if (ex.focalLength)
    rows.push([
      t('viewer:info.focal'),
      ex.focalLength35 ? `${ex.focalLength} mm (${ex.focalLength35} mm)` : `${ex.focalLength} mm`,
    ])
  if (ex.flash !== undefined)
    rows.push([
      t('viewer:info.flash'),
      ex.flash ? t('viewer:info.flashOn') : t('viewer:info.flashOff'),
    ])
  if (ex.orientation && ex.orientation !== 1)
    rows.push([t('viewer:info.orientation'), String(ex.orientation)])
  if (ex.software) rows.push([t('viewer:info.software'), ex.software])
  return rows
}

/** "2024-04-30T18:05:00" (camera local time, no zone) → localised, zone-free. */
function exifDateLabel(s: string, lang: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})$/.exec(s)
  if (!m) return s
  const d = new Date(
    Number(m[1]),
    Number(m[2]) - 1,
    Number(m[3]),
    Number(m[4]),
    Number(m[5]),
    Number(m[6]),
  )
  return formatDateTimeFull(d, lang)
}
