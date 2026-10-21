import i18next from 'i18next'
import { toast } from 'sonner'
import { errorMessage } from '@/lib/errors'
import { sharesByPath } from './api'

type LooseT = (key: string, options?: Record<string, unknown>) => string
const rawT = i18next.t as unknown as LooseT
const t: LooseT = (key, options) => String(options ? rawT(key, options) : rawT(key))

/** "Salin link share" (PROMPT 18a task 7): copies the first active public
 *  link of the item; tells the user when there is none. */
export async function copyFirstShareLink(path: string): Promise<boolean> {
  try {
    const { items } = await sharesByPath(path)
    const active = items.find((s) => s.mode === 'public' && s.status === 'active' && s.url)
    if (!active) {
      toast.info(t('share:toast.noLink'))
      return false
    }
    await navigator.clipboard.writeText(active.url)
    toast.success(t('share:result.copied'))
    return true
  } catch (e) {
    toast.error(e instanceof DOMException ? t('share:toast.copyFailed') : errorMessage(e))
    return false
  }
}
