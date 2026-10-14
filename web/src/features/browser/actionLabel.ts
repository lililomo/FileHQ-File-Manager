import type { ActionDef } from '@/lib/actions'
import { formatKeys, shortcutById } from '@/lib/shortcuts'

export type LooseT = (key: string, options?: Record<string, unknown>) => string

export function actionLabel(t: LooseT, a: ActionDef): string {
  return t(`files:actions.${a.labelKey}`)
}

export function actionShortcut(a: ActionDef): string | null {
  const def = a.shortcut ? shortcutById(a.shortcut) : undefined
  return def ? formatKeys(def.keys).join('+') : null
}
