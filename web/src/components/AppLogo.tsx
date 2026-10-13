import { cn } from '@/lib/utils'

/** Small brand mark drawn with the accent colour. */
export function AppLogo({ className, size = 22 }: { className?: string; size?: number }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      aria-hidden="true"
      className={cn('shrink-0 text-accent', className)}
    >
      <path
        d="M3 7.5A2.5 2.5 0 0 1 5.5 5h4.2a2 2 0 0 1 1.5.7L12.6 7h5.9A2.5 2.5 0 0 1 21 9.5V17a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17z"
        fill="currentColor"
        fillOpacity="0.18"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
      <path d="M7 13h10M7 16h6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
    </svg>
  )
}
