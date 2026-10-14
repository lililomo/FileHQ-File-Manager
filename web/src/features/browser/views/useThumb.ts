import { useEffect, useRef, useState } from 'react'
import type { Item, ThumbSize } from '../api'
import { CancelToken, requestThumb, urlCache, type ThumbStatus } from './thumbQueue'

export interface ThumbState {
  status: ThumbStatus
  src: string | null
}

/** Shared IntersectionObserver (rootMargin 200 px, SPEC §8.4). */
let observer: IntersectionObserver | null = null
const handlers = new WeakMap<Element, (visible: boolean) => void>()
function observe(el: Element, cb: (visible: boolean) => void): () => void {
  if (typeof IntersectionObserver === 'undefined') {
    cb(true)
    return () => {}
  }
  if (!observer) {
    observer = new IntersectionObserver(
      (entries) => {
        for (const e of entries) handlers.get(e.target)?.(e.isIntersecting)
      },
      { rootMargin: '200px' },
    )
  }
  handlers.set(el, cb)
  observer.observe(el)
  return () => {
    handlers.delete(el)
    observer?.unobserve(el)
  }
}

/** Requests the thumbnail of an item once its cell is (nearly) visible and
 *  cancels when it scrolls away before loading. Returns the element ref
 *  callback and the current state. */
export function useThumb(
  item: Item,
  size: ThumbSize,
  enabled = true,
): [(el: Element | null) => void, ThumbState] {
  const cached = enabled && item.thumb ? urlCache.peek(item.path, item.etag, size) : null
  const [state, setState] = useState<ThumbState>(
    cached ? { status: 'ready', src: cached } : { status: 'idle', src: null },
  )
  const elRef = useRef<Element | null>(null)
  const [el, setEl] = useState<Element | null>(null)
  const wanted = enabled && item.thumb && !item.isDir && !item.isSymlink

  useEffect(() => {
    if (!wanted || !el) return
    let token: CancelToken | null = null
    let done = false
    const stop = observe(el, (visible) => {
      if (done) return
      if (visible && !token) {
        token = new CancelToken()
        void requestThumb({
          path: item.path,
          etag: item.etag,
          size,
          token,
          onStatus: (status, src) => {
            if (token?.cancelled) return
            setState({ status, src })
            if (status === 'ready' || status === 'failed') done = true
          },
        })
      } else if (!visible && token && !done) {
        token.cancel()
        token = null
        setState((s) => (s.status === 'ready' ? s : { status: 'idle', src: null }))
      }
    })
    return () => {
      stop()
      token?.cancel()
    }
  }, [el, wanted, item.path, item.etag, size])

  const ref = (node: Element | null) => {
    if (node !== elRef.current) {
      elRef.current = node
      setEl(node)
    }
  }
  return [ref, wanted ? state : { status: 'idle', src: null }]
}
