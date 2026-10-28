import { readonly, ref } from 'vue'

type Session = { username: string }
const sessionState = ref<Session | null>(null)

/** The authenticated account; updates go through setSession. */
export const session = readonly(sessionState)

/** Replaces the account after login, logout or session verification. */
export function setSession(value: Session | null): void {
  sessionState.value = value
}

// getRandomValues also works over HTTP on a LAN, where randomUUID is unavailable.
export function newRequestKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  const hex = Array.from(bytes, (byte, i) => {
    if (i === 6) byte = (byte & 0x0f) | 0x40
    if (i === 8) byte = (byte & 0x3f) | 0x80
    return byte.toString(16).padStart(2, '0')
  }).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export type ApiErrorInit = {
  status: number // HTTP status; 0 for client-side failures
  code: string // server error.code, or a client code
  requestId: string // X-Request-ID; empty when unavailable
  details?: unknown
  retryAfterMs?: number // parsed Retry-After
  processingLog?: string // numbering diagnostics sent with the error
}

/**
 * A failed request. message is English developer text; user text comes from
 * errorText in errors.ts.
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string
  readonly details: unknown
  readonly retryAfterMs: number | undefined
  readonly processingLog: string

  constructor(message: string, init: ApiErrorInit) {
    super(message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.requestId = init.requestId
    this.details = init.details
    this.retryAfterMs = init.retryAfterMs
    this.processingLog = init.processingLog ?? ''
  }
}

export type ApiOptions = {
  method?: string
  body?: unknown
  signal?: AbortSignal
  headers?: Record<string, string>
}

type ErrorEnvelope = {
  error?: { code?: unknown; message?: unknown; details?: unknown }
}

function retryAfterMs(value: string | null): number | undefined {
  if (!value) return undefined
  const seconds = Number(value)
  if (Number.isFinite(seconds)) return seconds * 1000
  const at = Date.parse(value)
  return Number.isNaN(at) ? undefined : Math.max(0, at - Date.now())
}

// The slowest endpoint, a model probe, waits at most 15 s; a request still
// open after this is treated as lost.
const requestTimeoutMs = 60_000
const maxRetries = 2
const maxRetryWaitMs = 10_000

/** Maps a failed fetch: a caller's cancel stays a native AbortError. */
function clientFailure(error: unknown, signal: AbortSignal | undefined, timeout: AbortSignal) {
  if (signal?.aborted) return error
  if (timeout.aborted)
    return new ApiError('request timed out', { status: 0, code: 'timeout', requestId: '' })
  if (error instanceof TypeError)
    return new ApiError('network request failed', {
      status: 0,
      code: 'network_unavailable',
      requestId: ''
    })
  return error
}

/**
 * Reads and keyed writes retry network failures, timeouts, 429
 * and 503 at most twice; a Retry-After over 10 s returns the error at once.
 */
function retryDelayMs(error: unknown, attempt: number): number | undefined {
  if (attempt >= maxRetries || !(error instanceof ApiError)) return undefined
  const transient =
    error.code === 'network_unavailable' ||
    error.code === 'timeout' ||
    error.status === 429 ||
    error.status === 503
  if (!transient) return undefined
  if (error.retryAfterMs !== undefined)
    return error.retryAfterMs > maxRetryWaitMs ? undefined : error.retryAfterMs
  return Math.min(maxRetryWaitMs, 500 * 2 ** attempt * (1 + Math.random() * 0.2))
}

function sleep(milliseconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const aborted = () => new DOMException('request aborted', 'AbortError')
    if (signal?.aborted) {
      reject(aborted())
      return
    }
    const stop = () => {
      clearTimeout(timer)
      reject(aborted())
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', stop)
      resolve()
    }, milliseconds)
    signal?.addEventListener('abort', stop, { once: true })
  })
}

/** Sends a request to the Go server; the response is asserted to be T. */
export async function api<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const method = (options.method ?? 'GET').toUpperCase()
  const headers = new Headers(options.headers)
  const keyedPost = method === 'POST' && !path.startsWith('/auth/')
  if (keyedPost && !headers.get('Idempotency-Key')) {
    headers.set('Idempotency-Key', newRequestKey())
  }
  options = { ...options, method, headers: Object.fromEntries(headers) }
  const retryable = method === 'GET' || (keyedPost && Boolean(headers.get('Idempotency-Key')))
  for (let attempt = 0; ; attempt++) {
    try {
      return await send<T>(path, options)
    } catch (error: unknown) {
      const delay = retryable ? retryDelayMs(error, attempt) : undefined
      if (delay === undefined) throw error
      await sleep(delay, options.signal)
    }
  }
}

async function send<T>(path: string, options: ApiOptions): Promise<T> {
  const { method = 'GET', body, signal, headers = {} } = options
  const timeout = AbortSignal.timeout(requestTimeoutMs)
  let response: Response
  try {
    response = await fetch(`/api${path}`, {
      method,
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
      headers: {
        'Content-Type': method === 'PATCH' ? 'application/merge-patch+json' : 'application/json',
        ...headers
      },
      credentials: 'same-origin',
      ...(body === undefined ? {} : { body: JSON.stringify(body) })
    })
  } catch (error: unknown) {
    throw clientFailure(error, signal, timeout)
  }
  const requestId = response.headers.get('X-Request-ID') ?? ''
  if (response.status === 401 && !signal?.aborted && !timeout.aborted) setSession(null)
  let data: unknown
  try {
    const text = await response.text()
    // 204 and an accepted request without a status body have nothing to parse.
    if (text === '' && response.ok) return undefined as T
    data = JSON.parse(text)
  } catch (error: unknown) {
    if (signal?.aborted || timeout.aborted) throw clientFailure(error, signal, timeout)
    throw new ApiError('response is not JSON', {
      status: response.status,
      code: 'invalid_response',
      requestId
    })
  }
  if (!response.ok) {
    const envelope = (typeof data === 'object' && data !== null ? data : {}) as ErrorEnvelope
    const error = envelope.error ?? {}
    throw new ApiError(
      typeof error.message === 'string' ? error.message : `HTTP ${response.status}`,
      {
        status: response.status,
        code: typeof error.code === 'string' ? error.code : '',
        requestId,
        details: error.details,
        retryAfterMs: retryAfterMs(response.headers.get('Retry-After')),
        processingLog: processingLogOf(error.details)
      }
    )
  }
  return data as T
}

/** Reads a list response and returns its items. */
export async function apiItems<T>(path: string, options: ApiOptions = {}): Promise<T[]> {
  return (await api<{ items: T[] }>(path, options)).items
}

/** Returns the diagnostic a numbering failure carries in details.processing_log. */
function processingLogOf(details: unknown): string {
  if (typeof details !== 'object' || details === null || !('processing_log' in details)) return ''
  return typeof details.processing_log === 'string' ? details.processing_log : ''
}

/**
 * Sends a POST that may outlive the page, such as cancelling work on pagehide.
 * Failures are ignored: the server also expires abandoned work.
 */
export function apiOnExit(path: string, body: unknown = {}): void {
  void fetch(`/api${path}`, {
    method: 'POST',
    credentials: 'same-origin',
    keepalive: true,
    signal: AbortSignal.timeout(requestTimeoutMs),
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(body)
  }).catch(() => {
    // Page exit cannot show a failure; abandoned work also expires on the server.
  })
}

export async function bootstrap(signal?: AbortSignal) {
  try {
    const account = await api<Session>('/auth/session', { signal })
    if (!signal?.aborted) setSession(account)
  } catch {
    if (!signal?.aborted) setSession(null)
  }
}
