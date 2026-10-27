import { ref, type Ref } from 'vue'
import { api, ApiError, newRequestKey } from '../../app/api'
import { errorText } from '../../app/errors'
import type { Entry, ServerRecord } from '../../app/contracts'

type MarkResult = {
  key: string
  status: string
  changed: number
  unchanged: number
  failed: number
  interrupted: boolean
  failures: { name: string; error: ServerRecord }[]
}

// Only the server's own error body proves it rejected the request; a proxy page
// or unreadable body leaves the outcome unknown, so the job is queried again.
function serverRejected(e: unknown): e is ApiError {
  return e instanceof ApiError && e.code !== 'invalid_response'
}

type MarkOptions = {
  /** Shared with the page so marking never overlaps another write. */
  saving: Ref<boolean>
  /** Invalidates status polls that started before the submission. */
  beforeSubmit: () => void
  /** Refreshes entry states once the submission settles. */
  afterSubmit: () => Promise<void>
  notify: (message: string) => void
  disposed: () => boolean
}

// Marks run as keyed background tasks of at most 100 targets. A lost response
// is resumed by querying the same key; only an explicit rejection allows
// submitting that batch again.
export function useMarkEntries({
  saving,
  beforeSubmit,
  afterSubmit,
  notify,
  disposed
}: MarkOptions) {
  const marking = ref<Entry[]>([]),
    markValue = ref(true),
    markError = ref(''),
    markProgress = ref(''),
    markRetryUnavailable = ref(false)
  let markKeys: string[] = []
  function openMark(entries: Entry[], value: boolean) {
    if (saving.value) return
    marking.value = [...entries]
    markValue.value = value
    markError.value = ''
    markProgress.value = ''
    markRetryUnavailable.value = false
    markKeys = []
  }
  async function submitMark() {
    if (saving.value || markRetryUnavailable.value || !marking.value.length) return
    const entries = [...marking.value],
      markedValue = markValue.value
    beforeSubmit()
    saving.value = true
    markError.value = ''
    let changed = 0,
      unchanged = 0,
      failed = 0,
      confirmed = 0
    let stage: 'submit' | 'query' | 'interrupted' = 'submit',
      rejected = false
    const failures: string[] = []
    try {
      for (let i = 0; i < entries.length; i += 100) {
        const batchIndex = i / 100,
          existingKey = markKeys[batchIndex],
          requestKey = existingKey || (markKeys[batchIndex] = newRequestKey())
        markProgress.value = `正在处理第 ${i + 1}–${Math.min(i + 100, entries.length)} 个目标…`
        stage = existingKey ? 'query' : 'submit'
        let result: MarkResult
        if (existingKey) {
          result = await api<MarkResult>(
            `/entries/processed/status/${encodeURIComponent(requestKey)}`
          )
        } else {
          try {
            result = await api<MarkResult>('/entries/processed', {
              method: 'POST',
              body: {
                targets: entries.slice(i, i + 100).map((e) => e.id),
                marked: markedValue,
                key: requestKey
              }
            })
          } catch (e) {
            // A proxy or a lost response may fail after the task has started.
            // Only an explicit application rejection permits a new submission.
            if (serverRejected(e) && [400, 401, 403, 409, 429].includes(e.status)) {
              markKeys.splice(batchIndex, 1, '')
              rejected = true
            }
            throw e
          }
        }
        while (result.status === 'running') {
          confirmed = changed + result.changed
          markProgress.value = `后台处理中：已变更 ${changed + result.changed} 个文件，未变更 ${unchanged + result.unchanged} 个，失败 ${failed + result.failed} 项…`
          await new Promise((resolve) => setTimeout(resolve, 1000))
          if (disposed()) return
          stage = 'query'
          result = await api<MarkResult>(
            `/entries/processed/status/${encodeURIComponent(requestKey)}`
          )
        }
        changed += result.changed
        unchanged += result.unchanged
        failed += result.failed
        confirmed = changed
        failures.push(...result.failures.map((e) => `${e.name}：${errorText(e.error)}`))
        if (result.interrupted) {
          stage = 'interrupted'
          markRetryUnavailable.value = true
          throw new ApiError('mark job interrupted', {
            status: 0,
            code: 'mark_interrupted',
            requestId: ''
          })
        }
      }
      markKeys = []
      notify(
        `${markedValue ? '已标记' : '已取消标记'} ${changed} 个文件，未变更 ${unchanged} 个，失败 ${failed} 项`
      )
      if (failed) {
        markRetryUnavailable.value = true
        markError.value = [
          `已变更 ${changed} 个文件，未变更 ${unchanged} 个，失败 ${failed} 项；已完成的标记会保留。请关闭弹窗，重新选择失败目标后再操作。`,
          ...failures
        ].join('\n')
      } else marking.value = []
    } catch (e) {
      if (stage === 'query' && serverRejected(e) && e.status === 404) {
        markRetryUnavailable.value = true
        markError.value = `已确认变更 ${confirmed} 个文件；标记任务结果已不可查询（可能已回收、过期或服务重启）；已完成的标记会保留。请关闭弹窗，重新选择目标后再操作。`
      } else if (stage === 'interrupted') {
        markError.value = `已确认变更 ${confirmed} 个文件；${errorText(e)}；已完成的标记会保留。请关闭弹窗，重新选择未处理目标后再操作。`
        // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
      } else if (rejected) {
        markError.value = `已确认变更 ${confirmed} 个文件；${errorText(e)}；本批次未启动，可再次确认提交；已完成的标记会保留。`
      } else {
        markError.value = `已确认变更 ${confirmed} 个文件；${stage === 'query' ? '查询中断' : '提交结果未确认'}：${errorText(e)}。后台任务可能仍在执行，可再次确认查询原任务；已完成的标记会保留。`
      }
    } finally {
      markProgress.value = ''
      saving.value = false
      await afterSubmit()
    }
  }
  return { marking, markValue, markError, markProgress, markRetryUnavailable, openMark, submitMark }
}
