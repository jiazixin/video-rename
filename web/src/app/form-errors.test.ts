import { cleanup, render, waitFor } from '@testing-library/vue'
import { afterEach, expect, it } from 'vitest'
import { defineComponent, h, shallowRef, withDirectives, type Ref } from 'vue'
import userEvent from '@testing-library/user-event'
import { ApiError } from './api'
import { fieldErrors, vFormErrors } from './form-errors'

afterEach(cleanup)

function formComponent(error: Ref<unknown>, disabled = shallowRef(false)) {
  return defineComponent({
    setup() {
      return () =>
        withDirectives(
          h('form', [
            h('fieldset', { disabled: disabled.value }, [
              h('label', ['名称', h('input', { name: 'title', 'aria-describedby': 'title-help' })]),
              h('span', { id: 'title-help' }, 'Existing help'),
              h('label', ['并发', h('input', { name: 'workers' })])
            ])
          ]),
          [[vFormErrors, error.value]]
        )
    }
  })
}
const invalid = () =>
  new ApiError('private implementation detail', {
    status: 400,
    code: 'invalid_argument',
    requestId: 'req-fields',
    details: {
      fields: [
        { path: 'title', reason: 'invalid_length' },
        { path: 'workers', reason: 'out_of_range' }
      ]
    }
  })

it('associates multiple server errors with controls, focuses the first and clears edited fields', async () => {
  const error = shallowRef<unknown>()
  const view = render(formComponent(error))
  error.value = invalid()
  const title = view.getByRole('textbox', { name: '名称' })
  await waitFor(() => expect(title).toHaveAttribute('aria-invalid', 'true'))
  expect(title).toHaveFocus()
  expect(title.getAttribute('aria-describedby')).toContain('title-help field-error-')
  expect(view.container.querySelectorAll('.field-error')).toHaveLength(2)
  expect(view.container.textContent).not.toContain('private implementation detail')
  await userEvent.setup().type(title, 'Updated')
  expect(title).not.toHaveAttribute('aria-invalid')
  expect(title).toHaveAttribute('aria-describedby', 'title-help')
  expect(view.container.querySelectorAll('.field-error')).toHaveLength(1)
  error.value = undefined
  await waitFor(() => {
    expect(view.container.querySelectorAll('.field-error')).toHaveLength(0)
  })
})

it('renders initial errors when a form mounts and ignores malformed server field payloads', () => {
  const view = render(formComponent(shallowRef(invalid())))
  expect(view.container.querySelectorAll('.field-error')).toHaveLength(2)
  for (const fields of [
    null,
    'bad',
    [null, {}, { path: 1, reason: 'required' }, { path: 'name', reason: [] }]
  ]) {
    expect(
      fieldErrors(
        new ApiError('bad', {
          status: 400,
          code: 'invalid_argument',
          requestId: '',
          details: { fields }
        })
      ).size
    ).toBe(0)
  }
})

it('focuses validation only after the submitting fieldset is enabled again', async () => {
  const error = shallowRef<unknown>(),
    disabled = shallowRef(true)
  const view = render(formComponent(error, disabled))
  error.value = invalid()
  const title = view.getByRole('textbox', { name: '名称' })
  await waitFor(() => expect(title).toHaveAttribute('aria-invalid', 'true'))
  expect(title).not.toHaveFocus()
  disabled.value = false
  await waitFor(() => expect(title).toHaveFocus())
})
