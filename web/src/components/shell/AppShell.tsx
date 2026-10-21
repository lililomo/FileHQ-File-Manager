import { useCallback, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useQuotaSync } from '@/features/quota/useQuota'
import { TaskPanel } from '@/features/tasks/TaskPanel'
import { useTaskEvents } from '@/features/tasks/useTaskEvents'
import { TransferPanel } from '@/features/upload/TransferPanel'
import { UploadInputs, type UploadInputsHandle } from '@/features/upload/UploadInputs'
import { uploader } from '@/features/upload/useUploader'
import { useUploaderEffects } from '@/features/upload/useUploaderEffects'
import { MiniPlayer } from '@/features/viewer/audio/MiniPlayer'
import { useMiniPlayerSlot } from '@/features/viewer/audio/miniPlayerSlot'
import { cn } from '@/lib/utils'
import { usePlayerStore } from '@/stores/player'
import { GlobalOverlays } from './GlobalOverlays'
import { Sidebar } from './Sidebar'
import { TopBar } from './TopBar'

export function AppShell({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  useQuotaSync()
  useTaskEvents()
  // re-picking files for transfers left over from a previous page load
  const pickRef = useRef<UploadInputsHandle>(null)
  const pickAgain = useCallback(() => pickRef.current?.pickFiles(), [])
  useUploaderEffects(pickAgain)
  const playerShown = usePlayerStore((s) => s.index >= 0)
  // in the browser's slot the mini-player sits above the 28 px status bar
  const playerInSlot = !!useMiniPlayerSlot()
  return (
    <div className="flex h-dvh flex-col bg-bg text-text">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-50 focus:rounded-md focus:bg-accent focus:px-3 focus:py-2 focus:text-accent-fg"
      >
        {t('a11y.skipToContent')}
      </a>
      <TopBar />
      <div className="flex min-h-0 flex-1">
        <Sidebar />
        <main id="main" tabIndex={-1} className="min-w-0 flex-1 overflow-auto outline-none">
          {children}
        </main>
      </div>
      {/* SPEC §8.14: persistent mini-player — outlives folder/route changes */}
      <MiniPlayer />
      <GlobalOverlays />
      {/* SPEC §7.4 / §8.8: transfers above tasks, bottom-right; toasts stay bottom-left */}
      <div
        className={cn(
          'pointer-events-none fixed right-3 z-40 flex flex-col items-end gap-2 [&>*]:pointer-events-auto',
          playerShown ? (playerInSlot ? 'bottom-24' : 'bottom-17') : 'bottom-3',
        )}
      >
        <TransferPanel onPickAgain={pickAgain} />
        <TaskPanel />
      </div>
      <UploadInputs
        ref={pickRef}
        testId="upload-resume"
        onFiles={(files) => uploader.attach(Array.from(files))}
      />
    </div>
  )
}
