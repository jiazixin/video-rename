import { computed, ref, type Ref } from 'vue'
import { api, apiItems } from '../../app/api'
import { errorText } from '../../app/errors'
import type { Entry, Operation } from '../../app/contracts'
import { nameWithoutExtension, normalizedExtension } from './entry-types'

export type EntryDialogMode = 'rename' | 'auto' | 'extract' | 'restore' | 'history'
export type RunTask = 'rename' | 'extract'

/** Restores the latest rename of e. */
export function restoreRequest(e: Entry) {
  return api(`/operations/${e.restore_id}/restore`, {
    method: 'POST',
    body: {}
  })
}

type EntryDialogOptions = {
  /** Shared with the page so a submission never overlaps another write. */
  saving: Ref<boolean>
  /** Run settings shared with the batch dialog; reset when a run dialog opens. */
  modelConfigID: Ref<number>
  recursive: Ref<boolean>
  /** Invalidates status polls that started before the submission. */
  invalidateStatus: () => void
  queueRun: (task: RunTask, entries: Entry[]) => Promise<void>
  notify: (message: string) => void
  /** Refreshes entry states once the submission succeeds. */
  afterSubmit: () => Promise<void>
}

// One entry's rename, restore, run submission or naming history.
export function useEntryDialog(options: EntryDialogOptions) {
  const { saving, notify } = options
  const dialogEntry = ref<Entry>(),
    dialogMode = ref<EntryDialogMode | ''>(''),
    newName = ref(''),
    dialogError = ref(''),
    history = ref<Operation[]>([])
  // A history response applies only while its request is the latest one.
  let historyRequest = 0
  const extension = computed(() =>
    dialogEntry.value?.kind === 'file' ? normalizedExtension(dialogEntry.value.name) : ''
  )
  async function openEntryDialog(e: Entry, nextMode: EntryDialogMode) {
    if (saving.value) return
    const request = ++historyRequest
    if (nextMode === 'auto') options.modelConfigID.value = 0
    if (nextMode === 'extract') options.recursive.value = true
    dialogEntry.value = e
    dialogMode.value = nextMode
    dialogError.value = ''
    newName.value = e.kind === 'file' ? nameWithoutExtension(e.name) : e.name
    history.value = []
    if (nextMode !== 'restore' && nextMode !== 'history') return
    const current = () =>
      request === historyRequest && dialogMode.value === nextMode && dialogEntry.value?.id === e.id
    try {
      const records = await apiItems<Operation>(`/entries/${e.id}/history`)
      if (current()) history.value = records
    } catch (err) {
      if (current()) dialogError.value = errorText(err)
    }
  }
  function closeDialog() {
    historyRequest++
    dialogMode.value = ''
  }
  async function submit() {
    if (!dialogEntry.value) return
    options.invalidateStatus()
    saving.value = true
    dialogError.value = ''
    try {
      const e = dialogEntry.value
      if (dialogMode.value === 'rename') {
        await api<unknown>(`/entries/${e.id}/rename`, {
          method: 'POST',
          headers: { 'If-Match': e.etag },
          body: {
            name: newName.value + extension.value
          }
        })
        notify('名称已更新，历史已保存')
      } else if (dialogMode.value === 'restore') {
        await restoreRequest(e)
        notify('名称已还原，可以再次自动命名')
      } else {
        await options.queueRun(dialogMode.value === 'extract' ? 'extract' : 'rename', [e])
      }
      closeDialog()
      await options.afterSubmit()
    } catch (err) {
      dialogError.value = errorText(err)
    } finally {
      saving.value = false
    }
  }
  return {
    dialogEntry,
    dialogMode,
    newName,
    dialogError,
    history,
    extension,
    openEntryDialog,
    closeDialog,
    submit
  }
}
