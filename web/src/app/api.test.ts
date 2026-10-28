import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, bootstrap, session, setSession } from './api'
import { deferred } from '../test/deferred'

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
  setSession(null)
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
    setSession({ username: 'admin' })
    respond(401, { error: { code: 'unauthenticated', message: 'login required' } })
    await expect(api('/system')).rejects.toBeInstanceOf(ApiError)
    expect(session.value).toBeNull()
  })

  it('clears the session even when a 401 response body is malformed', async () => {
    setSession({ username: 'admin' })
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response('not JSON', { status: 401 })))
    )
    await expect(api('/system')).rejects.toMatchObject({ code: 'invalid_response' })
    expect(session.value).toBeNull()
  })

  it('ignores a retired bootstrap response after its owner cancels it', async () => {
    const response = deferred<Response>()
    vi.stubGlobal(
      'fetch',
      vi.fn(() => response.promise)
    )
    setSession({ username: 'current' })
    const controller = new AbortController()
    const result = bootstrap(controller.signal)
    controller.abort()
    response.resolve(Response.json({ error: { code: 'unauthenticated' } }, { status: 401 }))
    await result
    expect(session.value).toEqual({ username: 'current' })
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

  it('does not retry a Retry-After over ten seconds', async () => {
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

  it.each([undefined, { 'idempotency-key': '' }])(
    'retries a creation with the same generated request key and body (%j)',
    async (headers) => {
      vi.useFakeTimers()
      const fetchMock = vi
        .fn<typeof fetch>()
        .mockRejectedValueOnce(new TypeError('Failed to fetch'))
        .mockResolvedValueOnce(json(201, { id: 7 }))
      vi.stubGlobal('fetch', fetchMock)
      const result = api('/prompts', {
        method: 'POST',
        headers,
        body: { title: 'one', body: 'content' }
      })
      await vi.advanceTimersByTimeAsync(1000)
      await expect(result).resolves.toEqual({ id: 7 })
      expect(fetchMock).toHaveBeenCalledTimes(2)
      const first = fetchMock.mock.calls[0]?.[1]
      const second = fetchMock.mock.calls[1]?.[1]
      const key = new Headers(first?.headers).get('Idempotency-Key')
      expect(key).toMatch(/^[A-Za-z0-9_-]{8,128}$/)
      expect(new Headers(second?.headers).get('Idempotency-Key')).toBe(key)
      expect(second?.body).toBe(first?.body)
    }
  )

  it('sends validators and never retries authentication or unsupported keyed methods', async () => {
    const fetchMock = vi.fn<typeof fetch>(() =>
      Promise.resolve(json(503, { error: { code: 'unavailable', message: 'busy' } }))
    )
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      api('/prompts/1', {
        method: 'PATCH',
        headers: { 'If-Match': '"v1"' },
        body: { title: 'edited' }
      })
    ).rejects.toMatchObject({ status: 503 })
    const headers = new Headers(fetchMock.mock.calls[0]?.[1]?.headers)
    expect(headers.get('Content-Type')).toBe('application/merge-patch+json')
    expect(headers.get('If-Match')).toBe('"v1"')
    expect(headers.has('Idempotency-Key')).toBe(false)
    await expect(api('/auth/login', { method: 'POST', body: {} })).rejects.toMatchObject({
      status: 503
    })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).has('Idempotency-Key')).toBe(false)
    for (const [path, method] of [
      ['/auth/login', 'POST'],
      ['/auth/logout', 'POST'],
      ['/prompts/1', 'PATCH']
    ] as const) {
      await expect(
        api(path, { method, body: {}, headers: { 'Idempotency-Key': 'unsupported-key' } })
      ).rejects.toMatchObject({ status: 503, code: 'unavailable' })
    }
    expect(fetchMock).toHaveBeenCalledTimes(5)
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
