import { effectScope, nextTick, ref } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../app/api'
import { deferred } from '../../test/deferred'
import { useRunRecords } from './use-run-records'

vi.mock('../../app/api', async (original) => ({
  ...(await original<Record<string, unknown>>()),
  api: vi.fn()
}))

const page = (total: number) => ({
  items: [],
  directories: [],
  total,
  all_total: total,
  done_total: total,
  running_total: 0,
  skipped_total: 0,
  failed_total: 0,
  offset: 0,
  limit: 100
})

afterEach(() => {
  vi.clearAllMocks()
  vi.useRealTimers()
})

describe('useRunRecords', () => {
  it('aborts requests and cancels polling when its effect scope stops', async () => {
    vi.useFakeTimers()
    const pending = deferred<ReturnType<typeof page>>()
    vi.mocked(api).mockReturnValue(pending.promise)
    const scope = effectScope()
    const records = scope.run(() =>
      useRunRecords({ runID: () => 1, enabled: () => true, active: () => true })
    )
    if (!records) throw new Error('scope did not create records')
    const signal = vi.mocked(api).mock.calls[0]?.[1]?.signal
    scope.stop()
    expect(signal?.aborted).toBe(true)
    pending.resolve(page(7))
    await nextTick()
    expect(records.state.total).toBe(0)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('ignores a superseded filter response and stops polling after the terminal refresh', async () => {
    vi.useFakeTimers()
    const oldPage = deferred<ReturnType<typeof page>>()
    const newPage = deferred<ReturnType<typeof page>>()
    vi.mocked(api).mockReturnValueOnce(oldPage.promise).mockReturnValueOnce(newPage.promise)
    const active = ref(true)
    const scope = effectScope()
    const records = scope.run(() =>
      useRunRecords({ runID: () => 1, enabled: () => true, active: () => active.value })
    )
    if (!records) throw new Error('scope did not create records')
    records.filter.value = 'failed'
    await nextTick()
    expect(vi.mocked(api).mock.calls[0]?.[1]?.signal?.aborted).toBe(true)
    newPage.resolve(page(2))
    await nextTick()
    oldPage.resolve(page(100))
    await nextTick()
    expect(records.state.total).toBe(2)
    expect(vi.getTimerCount()).toBe(1)
    vi.mocked(api).mockResolvedValueOnce(page(3))
    active.value = false
    await nextTick()
    await nextTick()
    expect(records.state.total).toBe(3)
    expect(vi.getTimerCount()).toBe(0)
    scope.stop()
  })

  it('keeps loaded records visible when a refresh fails', async () => {
    vi.mocked(api).mockResolvedValueOnce(page(4))
    const scope = effectScope()
    const records = scope.run(() =>
      useRunRecords({ runID: () => 1, enabled: () => true, active: () => false })
    )
    if (!records) throw new Error('scope did not create records')
    await nextTick()
    vi.mocked(api).mockRejectedValueOnce(new Error('fixture unavailable'))
    await records.refresh()
    expect(records.state.total).toBe(4)
    expect(records.state.error).not.toBe('')
    expect(records.state.loaded).toBe(true)
    scope.stop()
  })
})
