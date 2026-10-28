import { effectScope } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useRunTiming } from './run-timing'

afterEach(() => {
  vi.useRealTimers()
})

describe('useRunTiming', () => {
  it('releases its clock when an effect scope stops', () => {
    vi.useFakeTimers()
    const scope = effectScope()
    scope.run(() => useRunTiming(() => []))
    expect(vi.getTimerCount()).toBe(1)
    scope.stop()
    expect(vi.getTimerCount()).toBe(0)
  })
})
