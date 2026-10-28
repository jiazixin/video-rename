import { effectScope, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../app/api'
import { deferred } from '../../test/deferred'
import { useLibraryScan } from './library-scan'

vi.mock('../../app/api', async (original) => ({
  ...(await original<Record<string, unknown>>()),
  api: vi.fn()
}))

afterEach(() => {
  vi.restoreAllMocks()
  vi.clearAllMocks()
  vi.useRealTimers()
})

describe('useLibraryScan', () => {
  it('releases its visibility listener and pending request when the scope stops', async () => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    const remove = vi.spyOn(document, 'removeEventListener')
    const pending = deferred<unknown>()
    vi.mocked(api).mockReturnValue(pending.promise)
    const scope = effectScope()
    const scan = scope.run(() => useLibraryScan())
    if (!scan) throw new Error('scope did not create scan')
    scan.startPolling()
    expect(api).toHaveBeenCalledOnce()
    const signal = vi.mocked(api).mock.calls[0]?.[1]?.signal
    scope.stop()
    expect(signal?.aborted).toBe(true)
    expect(remove).toHaveBeenCalledWith('visibilitychange', expect.any(Function))
    pending.resolve({ status: { status: 'idle' } })
    await nextTick()
    expect(scan.state.value).toBeUndefined()
    expect(vi.getTimerCount()).toBe(0)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(api).toHaveBeenCalledOnce()
  })

  it('cancels the next status poll when its scope stops after a successful response', async () => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    vi.mocked(api).mockResolvedValue({ status: { status: 'idle' } })
    const scope = effectScope()
    const scan = scope.run(() => useLibraryScan())
    if (!scan) throw new Error('scope did not create scan')
    scan.startPolling()
    await vi.waitFor(() => {
      expect(vi.getTimerCount()).toBe(1)
    })
    scope.stop()
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(10000)
    expect(api).toHaveBeenCalledOnce()
  })

  it('reports a failed companion refresh and keeps polling available', async () => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    vi.mocked(api).mockResolvedValue({ status: { status: 'idle' } })
    const scope = effectScope()
    const scan = scope.run(() =>
      useLibraryScan(() => Promise.reject(new Error('fixture refresh failed')))
    )
    if (!scan) throw new Error('scope did not create scan')
    scan.startPolling()
    await vi.waitFor(() => {
      expect(scan.state.value?.status.status).toBe('idle')
      expect(scan.error.value).not.toBe('')
      expect(vi.getTimerCount()).toBe(1)
    })
    scope.stop()
  })
})
