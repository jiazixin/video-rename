import { computed, ref, type ComputedRef, type Ref } from 'vue'
import { errorText } from '../../app/errors'
import type { Entry } from '../../app/contracts'
import { restoreRequest, type RunTask } from './entry-dialog'

export type BatchAction = 'auto' | 'extract' | 'restore'

type BatchOptions = {
  /** Shared with the page so a submission never overlaps another write. */
  saving: Ref<boolean>
  /** Run settings shared with the entry dialog; reset when the batch dialog opens. */
  modelConfigID: Ref<number>
  recursive: Ref<boolean>
  /** The page selection; restored entries leave it. */
  chosen: Ref<Map<number, Entry>>
  autoEntries: ComputedRef<Entry[]>
  extractEntries: ComputedRef<Entry[]>
  restoreEntries: ComputedRef<Entry[]>
  /** Invalidates status polls that started before the submission. */
  invalidateStatus: () => void
  queueRun: (task: RunTask, entries: Entry[]) => Promise<void>
  notify: (message: string) => void
  /** Refreshes entry states once the submission settles. */
  afterSubmit: () => Promise<void>
}

// Runs or restores for the selected entries. Restores go one at a time and keep
// the dialog open with the failures.
export function useBatchActions(options: BatchOptions) {
  const { saving, chosen, autoEntries, extractEntries, restoreEntries } = options
  const batchAction = ref<BatchAction | ''>(''),
    batchError = ref(''),
    batchProgress = ref('')
  const batchCount = computed(() => {
    if (batchAction.value === 'extract') return extractEntries.value.length
    if (batchAction.value === 'auto') return autoEntries.value.length
    return restoreEntries.value.length
  })
  function startBatch(action: BatchAction) {
    if (saving.value) return
    batchAction.value = action
    batchError.value = ''
    batchProgress.value = ''
    options.modelConfigID.value = 0
    options.recursive.value = true
  }
  async function restoreSelection() {
    const entries = [...restoreEntries.value]
    let done = 0
    const failures: string[] = []
    for (const e of entries) {
      batchProgress.value = `正在还原 ${++done} / ${entries.length}`
      try {
        await restoreRequest(e)
        chosen.value.delete(e.id)
      } catch (err) {
        failures.push(`${e.name}：${errorText(err)}`)
      }
    }
    options.notify(`已还原 ${entries.length - failures.length} 项，失败 ${failures.length} 项`)
    batchProgress.value = ''
    if (failures.length) batchError.value = failures.join('\n')
    else batchAction.value = ''
  }
  async function submitBatch() {
    if (saving.value) return
    options.invalidateStatus()
    saving.value = true
    batchError.value = ''
    try {
      if (batchAction.value === 'auto' || batchAction.value === 'extract') {
        const extracting = batchAction.value === 'extract'
        await options.queueRun(extracting ? 'extract' : 'rename', [
          ...(extracting ? extractEntries.value : autoEntries.value)
        ])
        batchAction.value = ''
      } else {
        await restoreSelection()
      }
      void options.afterSubmit()
    } catch (err) {
      batchError.value = errorText(err)
    } finally {
      saving.value = false
    }
  }
  return { batchAction, batchError, batchProgress, batchCount, startBatch, submitBatch }
}
