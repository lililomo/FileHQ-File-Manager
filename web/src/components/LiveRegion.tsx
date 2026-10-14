import { useTranslation } from 'react-i18next'

/** Global aria-live regions filled by `announce()` (SPEC §9.9). */
export function LiveRegion() {
  const { t } = useTranslation()
  return (
    // `aria-label` is prohibited on a role-less div (axe aria-prohibited-attr,
    // review B M-04): the wrapper is a named group, the children stay live.
    <div role="group" aria-label={t('a11y.announcements')} className="sr-only">
      <div id="live-polite" aria-live="polite" aria-atomic="true" />
      <div id="live-assertive" aria-live="assertive" aria-atomic="true" />
    </div>
  )
}
