import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

/** QR code of a link, rendered on a canvas with the lazy `qrcode` library
 *  (SPEC §8.15 "QR"; client-side so no Go dependency is needed). */
export function QrCode({
  value,
  size = 192,
  label,
}: {
  value: string
  size?: number
  /** Accessible name; defaults to the share-link label. */
  label?: string
}) {
  const { t } = useTranslation('share')
  const ref = useRef<HTMLCanvasElement>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let alive = true
    const canvas = ref.current
    if (!canvas) return
    import('qrcode')
      .then((m) => {
        if (!alive) return
        return m.toCanvas(canvas, value, { width: size, margin: 1, errorCorrectionLevel: 'M' })
      })
      .catch(() => alive && setFailed(true))
    return () => {
      alive = false
    }
  }, [value, size])
  if (failed) return <p className="text-xs text-text-muted">{t('result.qrFailed')}</p>
  return (
    <canvas
      ref={ref}
      width={size}
      height={size}
      role="img"
      aria-label={label ?? t('result.qrLabel')}
      className="rounded bg-white"
      data-testid="share-qr"
    />
  )
}
