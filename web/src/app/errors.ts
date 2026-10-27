import { ApiError } from './api'
import { ClipboardError } from '../lib/clipboard/clipboard'
import { hasCodeText, isRecord, recordText } from './server-codes'

// Text by status when no code table knows the code.
const statusTexts: Record<number, string> = {
  0: '网络连接失败，请检查网络后重试',
  400: '请求参数无效',
  401: '请先登录',
  403: '没有权限执行此操作',
  404: '请求的内容不存在',
  409: '当前状态不允许此操作，请刷新后重试',
  413: '请求内容过大',
  429: '操作过于频繁，请稍后重试',
  503: '服务暂时不可用，请稍后重试'
}

// Client-side failure codes: request failures (status 0) and clipboard failures.
const clientTexts: Record<string, string> = {
  invalid_response: '服务响应异常，请重试',
  network_unavailable: '网络连接失败，请检查网络后重试',
  timeout: '请求超时，请稍后重试',
  copy_failed: '复制失败',
  copy_busy: '正在复制，请稍后重试',
  copy_manual: '复制失败，请手动选取文本',
  mark_interrupted: '标记任务已中断，仍有目标未处理'
}

/**
 * Returns the user text of a failure: the caller's code table first, then the
 * server code catalog, then a text for the HTTP status. error may also be an
 * error or outcome record stored by the server (contracts.ts ServerRecord); a
 * record without a code, a missing record and an aborted request have no text.
 */
export function errorText(error: unknown, messages: Record<string, string> = {}): string {
  if (error === null || error === undefined) return ''
  if (error instanceof DOMException && error.name === 'AbortError') return ''
  if (error instanceof ApiError) return apiErrorText(error, messages)
  if (error instanceof ClipboardError) return messages[error.code] ?? clientTexts[error.code] ?? ''
  if (!(error instanceof Error) && isRecord(error)) {
    return (error.code && messages[error.code]) || recordText(error)
  }
  return '操作失败，请重试'
}

function apiErrorText(error: ApiError, messages: Record<string, string>): string {
  const own = messages[error.code] ?? clientTexts[error.code]
  if (own) return own
  if (error.code && hasCodeText(error.code)) {
    const details =
      typeof error.details === 'object' && error.details !== null
        ? (error.details as Record<string, unknown>)
        : undefined
    return recordText({ code: error.code, details })
  }
  return (
    statusTexts[error.status] ?? (error.status >= 500 ? '服务暂时无法完成请求' : '操作失败，请重试')
  )
}
