import { ref } from 'vue'
export const session = ref<{ username: string; csrf: string } | null>(null)
// getRandomValues also works over HTTP on a LAN, where randomUUID is unavailable.
export function key(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6]! & 0x0f) | 0x40
  bytes[8] = (bytes[8]! & 0x3f) | 0x80
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
export class ApiError extends Error {
  constructor(
    message: string,
    readonly processingLog = '',
    readonly status = 0
  ) {
    super(message)
    this.name = 'ApiError'
  }
}
export async function api<T = any>(
  path: string,
  method = 'GET',
  body?: unknown,
  signal?: AbortSignal
): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method,
    signal,
    headers: {
      'Content-Type': 'application/json',
      ...(session.value ? { 'X-CSRF-Token': session.value.csrf } : {})
    },
    credentials: 'same-origin',
    ...(body === undefined ? {} : { body: JSON.stringify(body) })
  })
  const data = await response.json().catch((error) => {
    if (signal?.aborted) throw error
    throw new Error('服务响应异常，请重试')
  })
  if (!response.ok) {
    if (response.status === 401) session.value = null
    throw new ApiError(data.message || '请求失败', data.processing_log || '', response.status)
  }
  return data
}
export async function bootstrap() {
  try {
    session.value = await api('/auth/session')
  } catch {
    session.value = null
  }
}
