import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, session } from './api'

function respond(status: number, body: unknown, headers: Record<string, string> = {}) {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(body), {
          status,
          headers: { 'Content-Type': 'application/json', ...headers }
        })
      )
    )
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  session.value = null
})

describe('api', () => {
  it('returns the response body', async () => {
    respond(200, { id: 1 })
    await expect(api<{ id: number }>('/runs/1')).resolves.toEqual({ id: 1 })
  })

  it('maps the error envelope to ApiError', async () => {
    respond(
      409,
      {
        error: { code: 'entry_busy', message: 'busy', details: { run: 3, processing_log: 'log' } }
      },
      { 'X-Request-ID': 'req-1', 'Retry-After': '2' }
    )
    const error = await api('/entries/1').catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({
      status: 409,
      code: 'entry_busy',
      message: 'busy',
      requestId: 'req-1',
      details: { run: 3, processing_log: 'log' },
      retryAfterMs: 2000,
      processingLog: 'log'
    })
  })

  it('clears the session on 401', async () => {
    session.value = { username: 'admin' }
    respond(401, { error: { code: 'unauthenticated', message: 'login required' } })
    await expect(api('/system')).rejects.toBeInstanceOf(ApiError)
    expect(session.value).toBeNull()
  })

  it('keeps an aborted request as AbortError', async () => {
    const controller = new AbortController()
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new DOMException('aborted', 'AbortError')))
    )
    controller.abort()
    const error = await api('/system', { signal: controller.signal }).catch((e: unknown) => e)
    expect(error).toBeInstanceOf(DOMException)
    expect((error as DOMException).name).toBe('AbortError')
  })
})

describe('api retries', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  function json(status: number, body: unknown, headers: Record<string, string> = {}) {
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json', ...headers }
    })
  }

  it('retries a read after a network failure and a 503, then succeeds', async () => {
    vi.useFakeTimers()
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockResolvedValueOnce(json(503, { error: { code: 'unavailable', message: 'busy' } }))
      .mockResolvedValueOnce(json(200, { ok: true }))
    vi.stubGlobal('fetch', fetchMock)
    const result = api<{ ok: boolean }>('/system')
    await vi.advanceTimersByTimeAsync(2_000)
    await expect(result).resolves.toEqual({ ok: true })
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('gives up after two retries with network_unavailable', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn(() => Promise.reject(new TypeError('Failed to fetch')))
    vi.stubGlobal('fetch', fetchMock)
    const result = api('/system').catch((error: unknown) => error)
    await vi.advanceTimersByTimeAsync(5_000)
    await expect(result).resolves.toMatchObject({ status: 0, code: 'network_unavailable' })
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('does not retry writes or a Retry-After over ten seconds', async () => {
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        json(503, { error: { code: 'unavailable', message: 'busy' } }, { 'Retry-After': '30' })
      )
    )
    vi.stubGlobal('fetch', fetchMock)
    await expect(api('/runs', { method: 'POST', body: {} })).rejects.toMatchObject({ status: 503 })
    await expect(api('/system')).rejects.toMatchObject({ status: 503 })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('maps a timed-out request to the timeout code', async () => {
    vi.useFakeTimers()
    vi.spyOn(AbortSignal, 'timeout').mockReturnValue(
      AbortSignal.abort(new DOMException('timed out', 'TimeoutError'))
    )
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new DOMException('timed out', 'TimeoutError')))
    )
    const result = api('/system').catch((error: unknown) => error)
    await vi.advanceTimersByTimeAsync(5_000)
    await expect(result).resolves.toMatchObject({ status: 0, code: 'timeout' })
  })
})
