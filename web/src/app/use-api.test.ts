import { effectScope } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'
import { useApi } from './use-api'
import { deferred } from '../test/deferred'

vi.mock('./api', async (original) => ({
  ...(await original<Record<string, unknown>>()),
  api: vi.fn()
}))

afterEach(() => vi.resetAllMocks())

describe('useApi', () => {
  it('cancels an in-flight request and rejects a late response when the scope stops', async () => {
    const response = deferred<{ items: number[] }>()
    vi.mocked(api).mockReturnValue(response.promise)
    const scope = effectScope()
    const client = scope.run(useApi)
    if (!client) throw new Error('request scope was not created')
    const result = client.apiItems<number>('/notifications/history')
    const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' })
    scope.stop()
    expect(vi.mocked(api).mock.calls[0]?.[1]?.signal?.aborted).toBe(true)
    response.resolve({ items: [1] })
    await rejected
  })

  it('does not send a request after its owner has stopped', async () => {
    const scope = effectScope()
    const client = scope.run(useApi)
    if (!client) throw new Error('request scope was not created')
    scope.stop()
    await expect(client.api<unknown>('/runs')).rejects.toMatchObject({ name: 'AbortError' })
    expect(api).not.toHaveBeenCalled()
  })

  it('cancels a superseded query without cancelling the next query', async () => {
    const oldResponse = deferred<{ id: number }>()
    vi.mocked(api).mockReturnValueOnce(oldResponse.promise).mockResolvedValueOnce({ id: 2 })
    const scope = effectScope()
    try {
      const client = scope.run(useApi)
      if (!client) throw new Error('request scope was not created')
      const controller = new AbortController()
      const oldResult = client.api<{ id: number }>('/runs/1', { signal: controller.signal })
      const rejected = expect(oldResult).rejects.toMatchObject({ name: 'AbortError' })
      controller.abort()
      await expect(client.api<{ id: number }>('/runs/2')).resolves.toEqual({ id: 2 })
      oldResponse.resolve({ id: 1 })
      await rejected
      expect(vi.mocked(api).mock.calls[0]?.[1]?.signal?.aborted).toBe(true)
      expect(vi.mocked(api).mock.calls[1]?.[1]?.signal?.aborted).toBe(false)
    } finally {
      scope.stop()
    }
  })
})
