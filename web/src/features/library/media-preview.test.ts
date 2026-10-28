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
  size_bytes: 1,
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
    queue.resolve({ items: [entry], offset: 0, limit: 100, total: 1, generation: '1' })
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

  for (const changed of [false, true])
    it(`uses offset pagination for audio switching${changed ? ' and refuses a changed directory' : ''}`, async () => {
      const tracks = Array.from({ length: 201 }, (_, index) => ({
        ...entry,
        id: index + 1,
        name: `track${index + 1}.wav`
      }))
      const first = tracks[199]
      if (!first) throw new Error('audio fixture is empty')
      let lastPageReads = 0
      vi.mocked(api).mockImplementation((path) => {
        const params = new URL(path, 'http://fixture.test').searchParams
        expect(params.has('page')).toBe(false)
        expect(params.has('generation')).toBe(false)
        const offset = Number(params.get('offset'))
        const page = offset / 200 + 1
        if (page === 2) lastPageReads++
        return Promise.resolve({
          items: tracks.slice((page - 1) * 200, page * 200),
          offset,
          limit: 200,
          total: tracks.length,
          generation: changed && page === 2 ? '2' : '1'
        })
      })
      const scope = effectScope()
      try {
        const media = scope.run(() =>
          useMediaPreview(() => ({ directory: 1, search: '', status: '', sort: 'name_asc' }))
        )
        if (!media) throw new Error('scope did not create media preview')
        media.playAudio(first)
        await media.switchAudio(false)
        expect(lastPageReads).toBeGreaterThan(0)
        expect(media.audioPlaying.value?.id).toBe(changed ? 200 : 201)
        expect(media.audioNotice.value).toBe(changed ? '目录已变化，请重新预览' : '')
        if (changed) expect(media.audioQueue.value).toBeNull()
        else expect(media.audioQueue.value?.items).toHaveLength(201)
      } finally {
        scope.stop()
      }
    })
})
