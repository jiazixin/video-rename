import { ref, type Ref } from 'vue'
import { api } from '../../app/api'
import { errorText } from '../../app/errors'
import type { Entry } from '../../app/contracts'

type DeleteOptions = {
  /** Shared with the page so a delete never overlaps another write. */
  saving: Ref<boolean>
  /** The page selection; deleted entries leave it. */
  chosen: Ref<Map<number, Entry>>
  /** Breadcrumb names below the library root, for the confirmation path. */
  directoryNames: () => string[]
  /** Invalidates status polls that started before the delete. */
  invalidateStatus: () => void
  /** Stops the scheduled status poll while a batch deletes. */
  pauseSync: () => void
  directory: () => number
  reload: () => Promise<void>
  /** Failed batch entries stay open for retry only while the page is shown. */
  keepFailures: () => boolean
  resetSelectionAnchor: () => void
  notify: (message: string) => void
  disposed: () => boolean
}

function deleteRequest(e: Entry) {
  return api(`/entries/${e.id}`, {
    method: 'DELETE',
    body: { version: e.version, confirmed: true }
  })
}

// Permanent deletes of one entry, or of the selection one entry at a time so a
// failure keeps the remaining entries for retry.
export function useDeleteEntries(options: DeleteOptions) {
  const { saving, chosen, notify } = options
  const deleting = ref<Entry>(),
    deleteError = ref(''),
    deletePath = ref('')
  const deletingBatch = ref<Entry[]>([]),
    deleteBatchPath = ref(''),
    deleteBatchError = ref(''),
    deleteBatchProgress = ref('')
  function confirmDelete(e: Entry) {
    if (saving.value) return
    deleting.value = { ...e }
    deleteError.value = ''
    deletePath.value = [...options.directoryNames(), e.name].join('/')
  }
  async function deleteEntry() {
    if (!deleting.value || saving.value) return
    saving.value = true
    deleteError.value = ''
    options.invalidateStatus()
    try {
      const e = deleting.value
      await deleteRequest(e)
      chosen.value.delete(e.id)
      deleting.value = undefined
      notify(`已永久删除 ${e.name}`)
      await options.reload()
    } catch (err) {
      deleteError.value = errorText(err)
    } finally {
      saving.value = false
    }
  }
  function confirmBatchDelete() {
    if (saving.value || !chosen.value.size) return
    deletingBatch.value = [...chosen.value.values()].map((e) => ({ ...e }))
    deleteBatchPath.value = ['媒体库', ...options.directoryNames()].join(' / ')
    deleteBatchError.value = ''
    deleteBatchProgress.value = ''
  }
  async function deleteBatch() {
    if (saving.value || !deletingBatch.value.length) return
    const entries = [...deletingBatch.value],
      directory = options.directory(),
      failed: Entry[] = [],
      errors: string[] = []
    saving.value = true
    options.invalidateStatus()
    options.pauseSync()
    deleteBatchError.value = ''
    try {
      for (const [index, e] of entries.entries()) {
        deleteBatchProgress.value = `正在删除 ${index + 1} / ${entries.length}：${e.name}`
        try {
          await deleteRequest(e)
          chosen.value.delete(e.id)
        } catch (err) {
          failed.push(e)
          errors.push(`${e.name}：${errorText(err)}`)
        }
      }
      const summary = `已删除 ${entries.length - failed.length} 项，失败 ${failed.length} 项`
      notify(summary)
      deletingBatch.value = options.keepFailures() ? failed : []
      deleteBatchError.value = failed.length ? [summary, ...errors].join('\n') : ''
      options.resetSelectionAnchor()
      if (directory === options.directory() && !options.disposed()) await options.reload()
    } finally {
      saving.value = false
      deleteBatchProgress.value = ''
    }
  }
  function closeDeletes() {
    deleting.value = undefined
    deletingBatch.value = []
  }
  return {
    deleting,
    deleteError,
    deletePath,
    deletingBatch,
    deleteBatchPath,
    deleteBatchError,
    deleteBatchProgress,
    confirmDelete,
    deleteEntry,
    confirmBatchDelete,
    deleteBatch,
    closeDeletes
  }
}
