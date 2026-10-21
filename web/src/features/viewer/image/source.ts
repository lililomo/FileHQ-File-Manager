/** Non-component helpers of the image viewer (kept apart for fast refresh). */

/** Slideshow intervals of pref `viewer.slideshow_interval` (SPEC §8.18). */
export const SLIDESHOW_INTERVALS = [2, 5, 10] as const

/** Server-rendered preview (≤ 2048 px WebP, SPEC §8.9 `variant=preview`) of a
 *  signed media URL; the signature covers path/purpose/exp, `variant` is free. */
export function previewUrl(signed: string): string {
  return `${signed}${signed.includes('?') ? '&' : '?'}variant=preview`
}
