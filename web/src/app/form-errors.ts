import type { ObjectDirective } from 'vue'
import { ApiError } from './api'
import { errorText } from './errors'
import { fieldIssues } from './server-codes'

/** Maps validated HTTP field paths through the application's single text mapper. */
export function fieldErrors(error: unknown): Map<string, string> {
  if (!(error instanceof ApiError) || error.code !== 'invalid_argument') return new Map()
  const fields = fieldIssues(error.details)
  const messages = new Map<string, string>()
  for (const field of fields) {
    const text = errorText(
      new ApiError('invalid field', {
        status: 400,
        code: error.code,
        requestId: error.requestId,
        details: { fields: [field] }
      })
    )
    messages.set(field.path, [messages.get(field.path), text].filter(Boolean).join('；'))
  }
  return messages
}

type Feedback = {
  path: string
  node: HTMLElement
  control: HTMLElement
  describedBy: string | null
  invalid: string | null
}
type FormState = {
  error: unknown
  needsFocus: boolean
  dismissed: Set<string>
  feedback: Feedback[]
  onInput: (event: Event) => void
}
const states = new WeakMap<HTMLElement, FormState>()
let nextID = 0

function clearFeedback(state: FormState) {
  for (const item of state.feedback) {
    item.node.remove()
    if (item.describedBy === null) item.control.removeAttribute('aria-describedby')
    else item.control.setAttribute('aria-describedby', item.describedBy)
    if (item.invalid === null) item.control.removeAttribute('aria-invalid')
    else item.control.setAttribute('aria-invalid', item.invalid)
  }
  state.feedback = []
}

function render(form: HTMLElement, error: unknown) {
  const state = states.get(form)
  if (!state) return
  const isNewError = error !== state.error
  if (isNewError) {
    state.dismissed.clear()
    state.needsFocus = error !== undefined
  }
  state.error = error
  clearFeedback(state)
  const fields = fieldErrors(error)
  const targets = [...form.querySelectorAll<HTMLElement>('[name], [data-field]')]
  for (const [path, text] of fields) {
    if (!path || state.dismissed.has(path)) continue
    const target =
      targets.find(
        (element) => (element.getAttribute('data-field') ?? element.getAttribute('name')) === path
      ) ??
      targets.find((element) =>
        path.startsWith(`${element.getAttribute('data-field') ?? element.getAttribute('name')}.`)
      )
    if (!target) continue // The form-level alert still reports an unknown or currently hidden field.
    const control = target.matches('input,select,textarea,button')
      ? target
      : target.querySelector<HTMLElement>('input,select,textarea,[role="combobox"],button')
    if (!control) continue
    const node = document.createElement('span')
    node.id = `field-error-${++nextID}`
    node.className = 'field-error'
    node.textContent = text
    const describedBy = control.getAttribute('aria-describedby')
    const invalid = control.getAttribute('aria-invalid')
    control.setAttribute('aria-describedby', [describedBy, node.id].filter(Boolean).join(' '))
    control.setAttribute('aria-invalid', 'true')
    const label = target.closest('label')
    ;(label ?? target.parentElement)?.append(node)
    state.feedback.push({ path, node, control, describedBy, invalid })
  }
  const first = state.feedback[0]?.control
  // The request wrapper can report validation before the caller releases its disabled fieldset.
  // Keep focus pending until the next render enables the control.
  if (state.needsFocus && first && !first.matches(':disabled,[aria-disabled="true"]')) {
    first.focus({ preventScroll: true })
    state.needsFocus = document.activeElement !== first
  }
}

/** Associates server field errors with named form controls, including shared pickers. */
export const vFormErrors: ObjectDirective<HTMLElement, unknown> = {
  mounted(form, binding) {
    const state: FormState = {
      error: undefined,
      needsFocus: false,
      dismissed: new Set(),
      feedback: [],
      onInput: (event) => {
        for (const item of state.feedback)
          if (
            item.control === event.target ||
            item.control.contains(event.target instanceof Node ? event.target : null)
          )
            state.dismissed.add(item.path)
        render(form, state.error)
      }
    }
    states.set(form, state)
    form.addEventListener('input', state.onInput)
    form.addEventListener('change', state.onInput)
    render(form, binding.value)
  },
  updated: (form, binding) => {
    render(form, binding.value)
  },
  beforeUnmount(form) {
    const state = states.get(form)
    if (!state) return
    clearFeedback(state)
    form.removeEventListener('input', state.onInput)
    form.removeEventListener('change', state.onInput)
    states.delete(form)
  }
}
