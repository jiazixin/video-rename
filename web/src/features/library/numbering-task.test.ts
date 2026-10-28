import { effectScope, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, apiOnExit } from '../../app/api'
import { useNumberingTask } from './numbering-task'

vi.mock('../../app/api', async (original) => ({
  ...(await original<Record<string, unknown>>()),
  api: vi.fn(),
  apiOnExit: vi.fn(),
  newRequestKey: () => 'fixture-numbering-key'
}))

afterEach(() => {
  vi.restoreAllMocks()
  vi.clearAllMocks()
  vi.useRealTimers()
})

describe('useNumberingTask', () => {
  it('cancels its poll, request and pagehide subscription on scope disposal', async () => {
    vi.useFakeTimers()
    vi.mocked(api).mockResolvedValue({ key: 'fixture-numbering-key', status: 'running' })
    const remove = vi.spyOn(window, 'removeEventListener')
    const scope = effectScope()
    const task = scope.run(() => useNumberingTask())
    if (!task) throw new Error('scope did not create task')
    const run = task.run({ parent: 1, action: 'preview' })
    const rejected = expect(run).rejects.toMatchObject({ name: 'AbortError' })
    await nextTick()
    expect(vi.getTimerCount()).toBe(1)
    const signal = vi.mocked(api).mock.calls[0]?.[1]?.signal
    scope.stop()
    await rejected
    expect(signal?.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
    expect(apiOnExit).toHaveBeenCalledExactlyOnceWith(
      '/numbering/jobs/fixture-numbering-key/cancel'
    )
    expect(remove).toHaveBeenCalledWith('pagehide', expect.any(Function))
    expect(task.progress.value).toBe('')
  })
})
