import { ref, nextTick } from 'vue'
import { api, newRequestKey } from '../../app/api'
import type { Entry } from '../../app/contracts'
import { nameWithoutExtension, normalizedExtension } from './entry-types'

export function useInlineRename(options: { beforeSave: () => void; onSaved: () => Promise<void> }) {
  const inlineFullName = ref(false)
  const inlineEntry = ref<Entry>(),
    inlineInput = ref<HTMLInputElement>(),
    inlineName = ref(''),
    inlineExtension = ref(''),
    inlineVersion = ref(0),
    inlineSaving = ref(false),
    inlineError = ref('')
  function setInlineInput(el: unknown) {
    inlineInput.value = el instanceof HTMLInputElement ? el : undefined
  }
  function cancelInlineRename() {
    if (inlineSaving.value) return
    inlineEntry.value = undefined
    inlineError.value = ''
  }
  async function beginInlineRename(e: Entry) {
    if (e.kind !== 'file' || inlineSaving.value) return
    inlineFullName.value = false
    inlineEntry.value = e
    inlineVersion.value = e.version
    inlineError.value = ''
    inlineExtension.value = normalizedExtension(e.name)
    inlineName.value = nameWithoutExtension(e.name)
    await nextTick()
    inlineInput.value?.focus()
    inlineInput.value?.select()
  }
  async function editInlineExtension() {
    if (inlineSaving.value) return
    // Move focus before removing the suffix button so keyboard activation does
    // not look like leaving the editor.
    inlineInput.value?.focus()
    const start = inlineName.value.length + 1
    inlineName.value += inlineExtension.value
    inlineFullName.value = true
    await nextTick()
    inlineInput.value?.focus()
    inlineInput.value?.setSelectionRange(start, inlineName.value.length)
  }
  function inlineFocusOut(event: FocusEvent) {
    if (
      event.relatedTarget instanceof Node &&
      (event.currentTarget as HTMLElement).contains(event.relatedTarget)
    )
      return
    cancelInlineRename()
  }
  async function saveInlineRename() {
    const e = inlineEntry.value
    if (!e || inlineSaving.value) return
    if (!inlineName.value.trim()) {
      inlineError.value = '请输入文件名'
      return
    }
    const name = inlineName.value + (inlineFullName.value ? '' : inlineExtension.value)
    if (name === e.name) {
      cancelInlineRename()
      return
    }
    inlineSaving.value = true
    inlineError.value = ''
    options.beforeSave()
    try {
      await api(`/entries/${e.id}/rename`, 'POST', {
        name,
        version: inlineVersion.value,
        key: newRequestKey()
      })
      await options.onSaved()
      inlineEntry.value = undefined
    } catch (err) {
      inlineError.value = (err as Error).message
      await nextTick()
      inlineInput.value?.focus()
    } finally {
      inlineSaving.value = false
    }
  }

  return {
    inlineFullName,
    inlineEntry,
    inlineName,
    inlineExtension,
    inlineSaving,
    inlineError,
    setInlineInput,
    cancelInlineRename,
    beginInlineRename,
    editInlineExtension,
    inlineFocusOut,
    saveInlineRename
  }
}
