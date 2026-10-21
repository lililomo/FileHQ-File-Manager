import { useTranslation } from 'react-i18next'

/** Loading / error placeholders shared by the text viewers. */
export function TextState({
  kind,
  detail,
}: {
  kind: 'loading' | 'error' | 'binary' | 'tooLarge'
  detail?: string
}) {
  const { t } = useTranslation('viewer')
  const text =
    kind === 'loading'
      ? t('text.loading')
      : kind === 'binary'
        ? t('text.binary')
        : kind === 'tooLarge'
          ? t('text.tooLarge', { detail: detail ?? '' })
          : t('text.error')
  return (
    <div
      className="flex h-full items-center justify-center p-6 text-center text-sm text-text-muted"
      aria-busy={kind === 'loading' ? 'true' : undefined}
      role={kind === 'loading' ? undefined : 'alert'}
      data-testid={`text-state-${kind}`}
    >
      {text}
    </div>
  )
}
