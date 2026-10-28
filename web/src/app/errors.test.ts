import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import { errorText } from './errors'
import { ClipboardError } from '../lib/clipboard/clipboard'

const failure = (status: number, code: string, details?: unknown) =>
  new ApiError('failure', { status, code, requestId: '', details })

describe('errorText', () => {
  it('prefers the caller code table', () => {
    expect(errorText(failure(409, 'entry_busy'), { entry_busy: '自定义文案' })).toBe('自定义文案')
  })

  it('maps server codes with details', () => {
    expect(errorText(failure(409, 'run_overlap', { run: 7 }))).toContain('#7')
  })

  it('maps field errors', () => {
    const error = failure(400, 'invalid_argument', {
      fields: [{ path: 'sampling.size_rules.1.frames', reason: 'out_of_range' }]
    })
    expect(errorText(error)).toBe('大小规则第 2 档：帧数须在 1–32 之间')
  })

  it('falls back to the status for unknown codes', () => {
    expect(errorText(failure(503, 'something_new'))).toBe('服务暂时不可用，请稍后重试')
  })

  it('has no text for an aborted request', () => {
    expect(errorText(new DOMException('aborted', 'AbortError'))).toBe('')
  })
})

describe('errorText for stored records', () => {
  it('has no text for a missing record or one without a code', () => {
    expect(errorText(undefined)).toBe('')
    expect(errorText({ message: 'no code' })).toBe('')
  })

  it('maps nested causes without exposing internal diagnostic text', () => {
    expect(
      errorText({
        code: 'numbering_rolled_back',
        details: { cause: { code: 'rename_conflict', details: { detail: 'EEXIST' } } }
      })
    ).toBe('操作失败，已恢复原名：目标已存在')
  })

  it('names the numbering phase and directory', () => {
    expect(
      errorText({ code: 'rename_conflict', details: { phase: 'final_rename', directory: 'a/b' } })
    ).toBe('a/b：最终编号失败：目标已存在')
  })

  it('treats a thrown Error as an unknown failure', () => {
    expect(errorText(new TypeError('boom'))).toBe('操作失败，请重试')
  })
})

describe('errorText for copy failures', () => {
  it('maps the clipboard code', () => {
    expect(errorText(new ClipboardError('copy_manual'))).toBe('复制失败，请手动选取文本')
  })
})
