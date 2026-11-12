import { nextTick } from 'vue'
import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ModalDialog from './ModalDialog.vue'

afterEach(() => {
  document.body.style.overflow = ''
  document.body.style.paddingRight = ''
  document.querySelectorAll('[data-fixture-trigger]').forEach((element) => {
    element.remove()
  })
})

describe('ModalDialog', () => {
  it('moves focus inside, emits Escape and restores the trigger and scroll styles', async () => {
    const user = userEvent.setup()
    const trigger = document.createElement('button')
    trigger.textContent = 'Open fixture'
    trigger.dataset.fixtureTrigger = ''
    document.body.append(trigger)
    trigger.focus()
    document.body.style.overflow = 'auto'
    document.body.style.paddingRight = '3px'
    const close = vi.fn()
    const dialog = render(ModalDialog, { props: { title: '测试弹窗', onClose: close } })
    await nextTick()
    expect(screen.getByRole('dialog', { name: '测试弹窗' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '关闭' })).toHaveFocus()
    expect(document.body.style.overflow).toBe('hidden')
    await user.keyboard('{Escape}')
    expect(close).toHaveBeenCalledOnce()
    dialog.unmount()
    expect(trigger).toHaveFocus()
    expect(document.body.style.overflow).toBe('auto')
    expect(document.body.style.paddingRight).toBe('3px')
    trigger.remove()
    document.body.style.overflow = ''
    document.body.style.paddingRight = ''
  })

  // A media viewer focuses its panel. Shift+Tab from there used to leave the
  // dialog for the page behind it.
  it('keeps Shift+Tab inside a dialog whose panel holds the focus', async () => {
    const user = userEvent.setup()
    // jsdom lays nothing out; the dialog treats elements without boxes as hidden.
    const rects = vi
      .spyOn(HTMLElement.prototype, 'getClientRects')
      .mockReturnValue([new DOMRect()] as unknown as DOMRectList)
    const trigger = document.createElement('button')
    trigger.textContent = 'Open fixture'
    trigger.dataset.fixtureTrigger = ''
    document.body.append(trigger)
    const dialog = render(ModalDialog, { props: { title: '媒体预览', theater: true } })
    await nextTick()
    expect(screen.getByRole('dialog', { name: '媒体预览' })).toHaveFocus()
    await user.tab({ shift: true })
    expect(screen.getByRole('button', { name: '关闭' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: '关闭' })).toHaveFocus()
    dialog.unmount()
    rects.mockRestore()
  })

  it('sends Escape to the top dialog and keeps scrolling locked until every dialog closes', async () => {
    const user = userEvent.setup()
    const closeFirst = vi.fn()
    const closeSecond = vi.fn()
    const first = render(ModalDialog, { props: { title: '第一个弹窗', onClose: closeFirst } })
    const second = render(ModalDialog, { props: { title: '第二个弹窗', onClose: closeSecond } })
    await nextTick()
    await user.keyboard('{Escape}')
    expect(closeFirst).not.toHaveBeenCalled()
    expect(closeSecond).toHaveBeenCalledOnce()
    first.unmount()
    expect(document.body.style.overflow).toBe('hidden')
    second.unmount()
    expect(document.body.style.overflow).toBe('')
  })
})
