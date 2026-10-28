import { afterEach, describe, expect, it, vi } from 'vitest'
import { deferred } from '../../test/deferred'
import { copyText } from './clipboard'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('copyText', () => {
  it('uses the Clipboard API in a secure context', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('isSecureContext', true)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    await copyText('fixture text')
    expect(writeText).toHaveBeenCalledExactlyOnceWith('fixture text')
  })

  it('reports manual copy when the origin is insecure or permission is rejected', async () => {
    const writeText = vi
      .fn()
      .mockRejectedValue(new DOMException('Permission denied', 'NotAllowedError'))
    vi.stubGlobal('isSecureContext', false)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    await expect(copyText('fixture')).rejects.toMatchObject({ code: 'copy_manual' })
    expect(writeText).not.toHaveBeenCalled()
    vi.stubGlobal('isSecureContext', true)
    await expect(copyText('fixture')).rejects.toMatchObject({ code: 'copy_manual' })
    expect(writeText).toHaveBeenCalledTimes(1)
  })

  it('rejects overlapping copies and releases the busy state after failure', async () => {
    const pending = deferred<undefined>()
    const writeText = vi.fn().mockReturnValueOnce(pending.promise).mockResolvedValue(undefined)
    vi.stubGlobal('isSecureContext', true)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const copying = copyText('first')
    const rejected = expect(copying).rejects.toMatchObject({ code: 'copy_manual' })
    await expect(copyText('second')).rejects.toMatchObject({ code: 'copy_busy' })
    pending.reject(new Error('permission rejected'))
    await rejected
    await copyText('third')
    expect(writeText.mock.calls).toEqual([['first'], ['third']])
  })
})
