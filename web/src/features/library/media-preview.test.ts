import { effectScope, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../app/api'
import type { Entry } from '../../app/contracts'
import { deferred } from '../../test/deferred'
import { useMediaPreview } from './media-preview'

vi.mock('../../app/api', async (original) => ({
  ...(await original<Record<string, unknown>>()),
  api: vi.fn()
}))

const entry: Entry = {
  id: 1,
  parent: 1,
  name: 'fixture.jpg',
  kind: 'file',
  size: 1,
  mtime: '2026-10-27T00:00:00.000Z',
  etag: '"v1"',
  version: 1,
  processed: '',
  restore_id: 0,
  task_status: ''
}
afterEach(() => {
  vi.clearAllMocks()
})

describe('useMediaPreview', () => {
  it('aborts queue and neighbour lookups when the scope stops', async () => {
    const queue = deferred<unknown>()
    const neighbour = deferred<Entry>()
    vi.mocked(api).mockReturnValueOnce(queue.promise).mockReturnValueOnce(neighbour.promise)
    const scope = effectScope()
    const media = scope.run(() =>
      useMediaPreview(() => ({ directory: 1, search: '', status: '', sort: 'name_asc' }))
    )
    if (!media) throw new Error('scope did not create media preview')
    media.openImage(entry)
    const switching = media.switchImage(false)
    const signals = vi.mocked(api).mock.calls.map((call) => call[1]?.signal)
    scope.stop()
    expect(signals).toHaveLength(2)
    for (const signal of signals) expect(signal?.aborted).toBe(true)
    queue.resolve({ items: [entry], next: 0 })
    neighbour.resolve({ ...entry, id: 2 })
    await switching
    await nextTick()
    expect(media.preview.value).toBeUndefined()
    expect(media.imageStrip.value).toEqual([])
    expect(media.imageBusy.value).toBe(false)
  })

  it('keeps a newly selected image when a previous neighbour response arrives late', async () => {
    const neighbour = deferred<Entry>()
    vi.mocked(api).mockReturnValueOnce(neighbour.promise)
    const scope = effectScope()
    const media = scope.run(() =>
      useMediaPreview(() => ({ directory: 1, search: '', status: '', sort: 'name_asc' }))
    )
    if (!media) throw new Error('scope did not create media preview')
    media.showImage(entry)
    const switching = media.switchImage(false)
    media.showImage({ ...entry, id: 3 })
    neighbour.resolve({ ...entry, id: 2 })
    await switching
    expect(media.preview.value?.id).toBe(3)
    expect(vi.mocked(api).mock.calls[0]?.[1]?.signal?.aborted).toBe(true)
    scope.stop()
  })
})
