import { effectScope, nextTick, ref } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../app/api'
import type { Entry } from '../../app/contracts'
import { deferred } from '../../test/deferred'
import { useMarkEntries } from './mark-entries'

vi.mock('../../app/api', async (original) => ({
  ...(await original<Record<string, unknown>>()),
  api: vi.fn()
}))

const entry: Entry = {
  id: 1,
  parent: 1,
  name: 'fixture.jpg',
  kind: 'file',
  size_bytes: 1,
  mtime: '2026-10-27T00:00:00.000Z',
  etag: '"v1"',
  version: 1,
  processed: '',
  restore_id: 0,
  task_status: ''
}
const running = {
  status: 'running',
  changed: 0,
  unchanged: 0,
  failed: 0,
  interrupted: false,
  failures: []
}

afterEach(() => {
  vi.clearAllMocks()
  vi.useRealTimers()
})

function setup() {
  const scope = effectScope()
  const afterSubmit = vi.fn<() => Promise<void>>().mockResolvedValue(undefined)
  const notify = vi.fn()
  const saving = ref(false)
  const mark = scope.run(() =>
    useMarkEntries({
      saving,
      beforeSubmit: vi.fn(),
      afterSubmit,
      notify,
      disposed: () => false
    })
  )
  if (!mark) throw new Error('scope did not create mark actions')
  mark.openMark([entry], true)
  return { scope, mark, saving, afterSubmit, notify }
}

describe('useMarkEntries', () => {
  it('cancels the poll timer and request when its scope stops', async () => {
    vi.useFakeTimers()
    vi.mocked(api).mockResolvedValue(running)
    const { scope, mark, saving, afterSubmit, notify } = setup()
    const submitting = mark.submitMark()
    await nextTick()
    expect(vi.getTimerCount()).toBe(1)
    const signal = vi.mocked(api).mock.calls[0]?.[1]?.signal
    scope.stop()
    await submitting
    expect(signal?.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
    expect(api).toHaveBeenCalledTimes(1)
    expect(saving.value).toBe(false)
    expect(afterSubmit).not.toHaveBeenCalled()
    expect(notify).not.toHaveBeenCalled()
  })

  it('ignores a late submission result after scope disposal', async () => {
    const result = deferred<unknown>()
    vi.mocked(api).mockReturnValue(result.promise)
    const { scope, mark, afterSubmit, notify } = setup()
    const submitting = mark.submitMark()
    scope.stop()
    result.resolve({ ...running, status: 'completed', changed: 1 })
    await submitting
    expect(mark.marking.value).toEqual([entry])
    expect(mark.markError.value).toBe('')
    expect(afterSubmit).not.toHaveBeenCalled()
    expect(notify).not.toHaveBeenCalled()
  })
})
