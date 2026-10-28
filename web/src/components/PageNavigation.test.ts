import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import PageNavigation from './PageNavigation.vue'

describe('PageNavigation', () => {
  it('validates jumps and supports Enter and Escape without changing selection on invalid input', async () => {
    const user = userEvent.setup()
    const change = vi.fn()
    render(PageNavigation, { props: { page: 2, totalPages: 5, onChange: change } })
    const input = screen.getByRole('textbox', { name: '跳转页码' })
    await user.clear(input)
    await user.type(input, '9{Enter}')
    expect(screen.getByRole('alert')).toHaveTextContent('请输入 1–5 之间的整数页码')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(change).not.toHaveBeenCalled()
    await user.keyboard('{Escape}')
    expect(input).toHaveValue('2')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    await user.clear(input)
    await user.type(input, '4{Enter}')
    expect(change).toHaveBeenCalledExactlyOnceWith(4)
  })

  it('disables navigation while loading and at empty-list boundaries', async () => {
    const view = render(PageNavigation, {
      props: { page: 1, totalPages: 0 }
    })
    for (const button of screen.getAllByRole('button')) expect(button).toBeDisabled()
    expect(screen.getByRole('textbox')).toBeDisabled()
    await view.rerender({ page: 2, totalPages: 5, disabled: true })
    for (const button of screen.getAllByRole('button')) expect(button).toBeDisabled()
  })
})
