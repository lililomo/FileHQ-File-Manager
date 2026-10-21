import { Maximize2, X } from 'lucide-react'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { IconButton } from '@/components/ui/icon-button'
import { currentTrack, usePlayerStore } from '@/stores/player'
import { SeekBar, Transport, VolumeControl } from './AudioControls'
import { Cover, PlayerDialog } from './AudioPlayer'
import { startAudioEngine } from './engine'

/** Persistent mini-player (SPEC §8.14: sticky bottom, 56 px): mounted in
 *  AppShell so playback and controls survive folder/route changes and
 *  dialogs; "open" expands the full player dialog. Hidden with an empty queue. */
export function MiniPlayer() {
  const { t } = useTranslation('viewer')
  const track = usePlayerStore(currentTrack)
  const meta = usePlayerStore((s) => s.meta)
  const setExpanded = usePlayerStore((s) => s.setExpanded)
  const clear = usePlayerStore((s) => s.clear)
  useEffect(() => startAudioEngine(), [])
  return (
    <>
      {track ? (
        <section
          className="flex h-14 shrink-0 items-center gap-3 border-t border-border bg-surface px-3"
          aria-label={t('audio.miniPlayer')}
          data-testid="mini-player"
        >
          <button
            type="button"
            className="flex min-w-0 items-center gap-2 rounded text-left focus-visible:outline-2 focus-visible:outline-accent sm:w-64"
            onClick={() => setExpanded(true)}
            title={t('audio.expand')}
          >
            <Cover className="size-10 shrink-0" />
            <span className="min-w-0">
              <span className="block truncate text-sm font-medium" data-testid="mini-player-title">
                {meta?.title || track.name}
              </span>
              <span className="block truncate text-xs text-text-muted">
                {meta?.artist || '\u00a0'}
              </span>
            </span>
          </button>
          <Transport size="sm" />
          <div className="hidden min-w-0 flex-1 md:block">
            <SeekBar compact />
          </div>
          <div className="hidden lg:block">
            <VolumeControl />
          </div>
          <div className="ml-auto flex items-center gap-0.5 md:ml-0">
            <IconButton
              label={t('audio.expand')}
              tooltipSide="top"
              onClick={() => setExpanded(true)}
              data-testid="mini-player-expand"
            >
              <Maximize2 aria-hidden="true" />
            </IconButton>
            <IconButton
              label={t('audio.stop')}
              tooltipSide="top"
              onClick={clear}
              data-testid="mini-player-close"
            >
              <X aria-hidden="true" />
            </IconButton>
          </div>
        </section>
      ) : null}
      <PlayerDialog />
    </>
  )
}
