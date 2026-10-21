import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { setMiniPlayerSlot, useMiniPlayerSlot } from './miniPlayerSlot'

describe('mini-player slot', () => {
  it('publishes the slot element to subscribers and clears it on unmount', () => {
    const { result } = renderHook(() => useMiniPlayerSlot())
    expect(result.current).toBeNull()
    const el = document.createElement('div')
    act(() => setMiniPlayerSlot(el))
    expect(result.current).toBe(el)
    act(() => setMiniPlayerSlot(el)) // same element: no change
    expect(result.current).toBe(el)
    act(() => setMiniPlayerSlot(null))
    expect(result.current).toBeNull()
  })
})
