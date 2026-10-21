import { Download, Music } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { useDownload } from '@/features/download/useDownload'
import { cn } from '@/lib/utils'
import { AUDIO_RATES, currentTrack, usePlayerStore } from '@/stores/player'
import { ModeButtons, SeekBar, Transport, VolumeControl } from './AudioControls'
import { Playlist } from './Playlist'

/** Cover art, or a music glyph while there is none. */
export function Cover({ className }: { className?: string }) {
  const meta = usePlayerStore((s) => s.meta)
  const { t } = useTranslation('viewer')
  return meta?.coverUrl ? (
    <img
      src={meta.coverUrl}
      alt={t('audio.cover')}
      className={cn('aspect-square rounded-md object-cover', className)}
      data-testid="audio-cover"
    />
  ) : (
    <div
      className={cn(
        'flex aspect-square items-center justify-center rounded-md bg-surface-2 text-text-muted',
        className,
      )}
      aria-hidden="true"
    >
      <Music className="size-1/3" />
    </div>
  )
}

/** Full music player (SPEC §8.14 AudioPlayer): cover, tags, transport,
 *  seek, volume, shuffle/repeat, speed and the playlist. Rendered in the
 *  global player dialog; the element itself is the engine's. */
export function AudioPlayer() {
  const { t } = useTranslation('viewer')
  const dl = useDownload()
  const track = usePlayerStore(currentTrack)
  const meta = usePlayerStore((s) => s.meta)
  const error = usePlayerStore((s) => s.error)
  const rate = usePlayerStore((s) => s.rate)
  const setRate = usePlayerStore((s) => s.setRate)
  const title = meta?.title || track?.name || t('audio.nothing')
  const sub = [meta?.artist, meta?.album, meta?.year].filter(Boolean).join(' · ')
  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row" data-testid="audio-player">
      <div className="flex min-w-0 flex-1 flex-col items-center justify-center gap-4 p-6">
        <Cover className="w-48 max-w-full sm:w-64" />
        <div className="w-full max-w-md text-center">
          <div className="truncate text-lg font-semibold" title={title} data-testid="audio-title">
            {title}
          </div>
          <div className="truncate text-sm text-text-muted" data-testid="audio-artist">
            {sub || '\u00a0'}
          </div>
        </div>
        {error && track ? (
          <div
            role="alert"
            className="flex max-w-md flex-col items-center gap-2 rounded-md border border-border bg-surface p-3 text-center text-sm"
            data-testid="audio-error"
          >
            <span>{error === 'unsupported' ? t('audio.unsupported') : t('audio.failed')}</span>
            <Button variant="outline" size="sm" onClick={() => void dl.downloadFile(track)}>
              <Download aria-hidden="true" />
              {t('card.download')}
            </Button>
          </div>
        ) : null}
        <div className="w-full max-w-md">
          <SeekBar />
        </div>
        <div className="flex w-full max-w-md flex-wrap items-center justify-between gap-2">
          <ModeButtons />
          <Transport />
          <label className="flex items-center gap-1 text-xs text-text-muted">
            <span className="sr-only">{t('audio.speed')}</span>
            <select
              value={rate}
              onChange={(e) => setRate(Number(e.target.value))}
              className="h-8 rounded-md border border-border bg-surface px-1 text-xs text-text"
              aria-label={t('audio.speed')}
              data-testid="audio-rate"
            >
              {AUDIO_RATES.map((r) => (
                <option key={r} value={r}>
                  {r}×
                </option>
              ))}
            </select>
          </label>
        </div>
        <VolumeControl />
      </div>
      <div className="flex max-h-[40vh] min-h-0 w-full flex-col border-t border-border md:max-h-none md:w-80 md:border-t-0 md:border-l">
        <Playlist />
      </div>
    </div>
  )
}

/** The expanded player as a dialog over any route (mini-player "open"). */
export function PlayerDialog() {
  const { t } = useTranslation('viewer')
  const expanded = usePlayerStore((s) => s.expanded)
  const setExpanded = usePlayerStore((s) => s.setExpanded)
  const track = usePlayerStore(currentTrack)
  const dl = useDownload()
  return (
    <Dialog open={expanded} onOpenChange={setExpanded}>
      <DialogContent
        className="flex h-[85vh] w-[90vw] max-w-4xl flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl"
        showCloseButton={false}
        // focus the dialog itself: a focused icon button would open its
        // tooltip, and the first Esc would only close that tooltip
        onOpenAutoFocus={(e) => {
          e.preventDefault()
          ;(e.currentTarget as HTMLElement | null)?.focus()
        }}
        data-testid="player-dialog"
      >
        <DialogTitle className="sr-only">{t('audio.title')}</DialogTitle>
        <DialogDescription className="sr-only">{t('audio.description')}</DialogDescription>
        <header className="flex h-12 shrink-0 items-center gap-1 border-b border-border bg-surface px-3">
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{t('audio.title')}</span>
          {track ? (
            <IconButton label={t('header.download')} onClick={() => void dl.downloadFile(track)}>
              <Download aria-hidden="true" />
            </IconButton>
          ) : null}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setExpanded(false)}
            data-testid="player-dialog-close"
          >
            {t('header.close')}
          </Button>
        </header>
        <div className="min-h-0 flex-1">
          <AudioPlayer />
        </div>
      </DialogContent>
    </Dialog>
  )
}
