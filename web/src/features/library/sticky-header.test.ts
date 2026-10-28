import { effectScope, ref } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useStickyHeader } from './sticky-header'

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('useStickyHeader', () => {
  it('disconnects observers and pending geometry work when its scope stops', () => {
    const disconnect = vi.fn()
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe = vi.fn()
        disconnect = disconnect
      }
    )
    const request = vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(17)
    const cancel = vi.spyOn(window, 'cancelAnimationFrame')
    const scope = effectScope()
    const header = scope.run(() =>
      useStickyHeader({
        fixedControls: ref(document.createElement('div')),
        fileTable: ref(document.createElement('table')),
        headerScroll: ref(document.createElement('div'))
      })
    )
    if (!header) throw new Error('scope did not create header')
    header.observeLayout()
    header.scheduleHeaderLayout()
    expect(request).toHaveBeenCalledOnce()
    scope.stop()
    expect(disconnect).toHaveBeenCalledOnce()
    expect(cancel).toHaveBeenCalledWith(17)
  })
})
