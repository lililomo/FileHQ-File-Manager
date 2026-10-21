import {
  Pause,
  Play,
  Repeat,
  Repeat1,
  Shuffle,
  SkipBack,
  SkipForward,
  Volume2,
  VolumeX,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { IconButton } from '@/components/ui/icon-button'
import { formatDuration } from '@/lib/format'
import { cn } from '@/lib/utils'
import { usePlayerStore } from '@/stores/player'
import { seekAudio } from './engine'

/** Shared transport buttons of the full player and the mini-player. */
export function Transport({ size = 'md' }: { size?: 'sm' | 'md' }) {
  const { t } = useTranslation('viewer')
  const playing = usePlayerStore((s) => s.playing)
  const has = usePlayerStore((s) => s.index >= 0)
  const prev = usePlayerStore((s) => s.prev)
  const next = usePlayerStore((s) => s.next)
  const toggle = usePlayerStore((s) => s.toggle)
  const icon = size === 'sm' ? 'size-4' : 'size-5'
  return (
    <div className="flex items-center gap-0.5">
      <IconButton
        label={t('audio.prev')}
        tooltipSide="top"
        onClick={prev}
        disabled={!has}
        data-testid="audio-prev"
      >
        <SkipBack aria-hidden="true" className={icon} />
      </IconButton>
      <IconButton
        label={playing ? t('audio.pause') : t('audio.play')}
        tooltipSide="top"
        onClick={toggle}
        disabled={!has}
        variant={size === 'md' ? 'primary' : 'ghost'}
        className={
          size === 'md' ? 'size-10 rounded-full text-accent-fg hover:text-accent-fg' : undefined
        }
        data-testid="audio-toggle"
        data-playing={playing ? 'true' : 'false'}
      >
        {playing ? (
          <Pause aria-hidden="true" className={icon} />
        ) : (
          <Play aria-hidden="true" className={icon} />
        )}
      </IconButton>
      <IconButton
        label={t('audio.next')}
        tooltipSide="top"
        onClick={() => next()}
        disabled={!has}
        data-testid="audio-next"
      >
        <SkipForward aria-hidden="true" className={icon} />
      </IconButton>
    </div>
  )
}

/** Seek bar with elapsed / total time. */
export function SeekBar({ compact = false }: { compact?: boolean }) {
  const { t } = useTranslation('viewer')
  const currentTime = usePlayerStore((s) => s.currentTime)
  const duration = usePlayerStore((s) => s.duration)
  return (
    <div
      className={cn(
        'flex min-w-0 items-center gap-2 text-xs text-text-muted tabular',
        compact && 'gap-1.5',
      )}
    >
      <span className="w-10 shrink-0 text-right" data-testid="audio-time">
        {formatDuration(Math.floor(currentTime))}
      </span>
      <input
        type="range"
        min={0}
        max={duration > 0 ? duration : 1}
        step={0.1}
        value={Math.min(currentTime, duration || 0)}
        disabled={duration <= 0}
        aria-label={t('audio.seek')}
        aria-valuetext={`${formatDuration(currentTime)} / ${formatDuration(duration)}`}
        onChange={(e) => seekAudio(Number(e.target.value))}
        className="fm-range min-w-0 flex-1"
        data-testid="audio-seek"
      />
      <span className="w-10 shrink-0" data-testid="audio-duration">
        {formatDuration(duration)}
      </span>
    </div>
  )
}

export function VolumeControl() {
  const { t } = useTranslation('viewer')
  const volume = usePlayerStore((s) => s.volume)
  const muted = usePlayerStore((s) => s.muted)
  const setVolume = usePlayerStore((s) => s.setVolume)
  const setMuted = usePlayerStore((s) => s.setMuted)
  return (
    <div className="flex items-center gap-1">
      <IconButton
        label={muted ? t('audio.unmute') : t('audio.mute')}
        tooltipSide="top"
        onClick={() => setMuted(!muted)}
        aria-pressed={muted}
        data-testid="audio-mute"
      >
        {muted || volume === 0 ? <VolumeX aria-hidden="true" /> : <Volume2 aria-hidden="true" />}
      </IconButton>
      <input
        type="range"
        min={0}
        max={1}
        step={0.05}
        value={muted ? 0 : volume}
        aria-label={t('audio.volume')}
        onChange={(e) => setVolume(Number(e.target.value))}
        className="fm-range w-24"
        data-testid="audio-volume"
      />
    </div>
  )
}

export function ModeButtons() {
  const { t } = useTranslation('viewer')
  const shuffle = usePlayerStore((s) => s.shuffle)
  const repeat = usePlayerStore((s) => s.repeat)
  const toggleShuffle = usePlayerStore((s) => s.toggleShuffle)
  const cycleRepeat = usePlayerStore((s) => s.cycleRepeat)
  return (
    <div className="flex items-center gap-0.5">
      <IconButton
        label={t('audio.shuffle')}
        tooltipSide="top"
        onClick={toggleShuffle}
        aria-pressed={shuffle}
        className={shuffle ? 'bg-surface-2 text-accent' : undefined}
        data-testid="audio-shuffle"
      >
        <Shuffle aria-hidden="true" />
      </IconButton>
      <IconButton
        label={t(`audio.repeat_${repeat}`)}
        tooltipSide="top"
        onClick={cycleRepeat}
        aria-pressed={repeat !== 'off'}
        className={repeat !== 'off' ? 'bg-surface-2 text-accent' : undefined}
        data-testid="audio-repeat"
        data-mode={repeat}
      >
        {repeat === 'one' ? <Repeat1 aria-hidden="true" /> : <Repeat aria-hidden="true" />}
      </IconButton>
    </div>
  )
}
