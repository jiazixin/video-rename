import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import ToastNotice from './ToastNotice.vue'

describe('ToastNotice', () => {
  it('announces each result and closes the chosen notice', async () => {
    const user = userEvent.setup()
    const close = vi.fn()
    render(ToastNotice, {
      props: {
        notices: [
          { id: 7, message: '已保存设置', tone: 'success' },
          { id: 8, message: '设置保存失败', tone: 'error' }
        ],
        animated: false,
        onClose: close
      }
    })
    expect(screen.getAllByRole('status')).toHaveLength(2)
    const closeSecond = screen.getAllByRole('button', { name: '关闭提示' })[1]
    if (!closeSecond) throw new Error('missing second notice')
    await user.click(closeSecond)
    expect(close).toHaveBeenCalledExactlyOnceWith(8)
  })
})
