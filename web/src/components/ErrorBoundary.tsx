import { Component, type ReactNode } from 'react'

/** Minimal error boundary (review D): a render error inside a lazy viewer
 *  (a pathological file, a library edge case) must not unmount the whole
 *  application — the overlay shows a short message instead and the rest of
 *  the UI keeps working. Resets when `resetKey` changes (next file). */
export class ErrorBoundary extends Component<
  { children: ReactNode; fallback: (error: Error) => ReactNode; resetKey?: unknown },
  { error: Error | null; key: unknown }
> {
  override state = { error: null as Error | null, key: this.props.resetKey }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  static getDerivedStateFromProps(
    props: { resetKey?: unknown },
    state: { error: Error | null; key: unknown },
  ) {
    return props.resetKey !== state.key ? { error: null, key: props.resetKey } : null
  }

  override componentDidCatch(error: Error) {
    console.error('viewer error', error)
  }

  override render() {
    return this.state.error ? this.props.fallback(this.state.error) : this.props.children
  }
}
