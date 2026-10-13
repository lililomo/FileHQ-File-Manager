import { useEffect } from 'react'
import { ShortcutHelp } from '@/features/browser/ShortcutHelp'
import { CommandPalette } from '@/features/command-palette/CommandPalette'
import { pushScope, registerShortcut } from '@/lib/shortcuts'
import { useOverlaysStore } from '@/stores/overlays'

/** App-wide overlays and their global shortcuts (Ctrl+K palette, ? help). */
export function GlobalOverlays() {
  const helpOpen = useOverlaysStore((s) => s.helpOpen)
  const setHelp = useOverlaysStore((s) => s.setHelp)
  const paletteOpen = useOverlaysStore((s) => s.paletteOpen)
  const setPalette = useOverlaysStore((s) => s.setPalette)
  useEffect(() => {
    const offs = [
      registerShortcut('palette', () => setPalette(!useOverlaysStore.getState().paletteOpen)),
      registerShortcut('help', () => setHelp(!useOverlaysStore.getState().helpOpen)),
    ]
    return () => offs.forEach((off) => off())
  }, [setHelp, setPalette])
  useEffect(() => {
    if (!helpOpen && !paletteOpen) return
    return pushScope('dialog')
  }, [helpOpen, paletteOpen])
  return (
    <>
      <CommandPalette />
      <ShortcutHelp open={helpOpen} onOpenChange={setHelp} />
    </>
  )
}
