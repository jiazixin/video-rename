import { ref, onScopeDispose, onDeactivated } from 'vue'
import { api, apiOnExit, ApiError, newRequestKey } from '../../app/api'
import type { ServerRecord } from '../../app/contracts'

type NumberingTask = {
  key: string
  status: string
  phase: string
  directory?: number
  directory_done?: number
  directory_total?: number
  directories: number
  files: number
  done: number
  total: number
  error?: ServerRecord
  processing_log: string
}
type NumberingInput = {
  parent: number
  action: 'preview' | 'apply'
  batches?: string[]
  preview?: string
}
export function useNumberingTask() {
  const progress = ref('')
  let active: { key: string; controller: AbortController } | undefined
  function cancel() {
    if (!active) return
    const task = active
    active = undefined
    task.controller.abort()
    apiOnExit(`/numbering/jobs/${task.key}/cancel`)
  }
  async function run({ parent, action, batches, preview }: NumberingInput) {
    const task = { key: newRequestKey(), controller: new AbortController() }
    active = task
    const signal = task.controller.signal
    progress.value = action === 'preview' ? '正在扫描目录并生成预览…' : '正在核对文件并编号…'
    try {
      let state = await api<NumberingTask>('/numbering/jobs', {
        method: 'POST',
        body: { parent, action, batches, preview },
        headers: { 'Idempotency-Key': task.key },
        signal
      })
      while (state.status === 'running' || state.status === 'cancelling') {
        progress.value =
          action === 'preview'
            ? `正在生成预览：已扫描 ${state.directories} 个目录、${state.files} 个文件${state.directory ? ` · 目录 #${state.directory}` : ''}`
            : `正在编号：已完成 ${state.done} / ${state.total} 个目录、${state.files} 个文件${state.directory_total ? ` · 当前目录 ${state.directory_done ?? 0} / ${state.directory_total} 个文件` : ''}`
        await new Promise<void>((resolve, reject) => {
          const abort = () => {
            clearTimeout(timer)
            reject(new DOMException('已停止', 'AbortError'))
          }
          const timer = setTimeout(() => {
            signal.removeEventListener('abort', abort)
            resolve()
          }, 700)
          signal.addEventListener('abort', abort, { once: true })
          if (signal.aborted) abort()
        })
        state = await api<NumberingTask>(`/numbering/jobs/${task.key}`, { signal })
      }
      if (state.status !== 'completed')
        throw new ApiError(`numbering ${action} stopped`, {
          status: 0,
          code: action === 'apply' ? 'numbering_apply_stopped' : 'numbering_preview_stopped',
          requestId: '',
          details: { done: state.done, files: state.files, cause: state.error },
          processingLog: state.processing_log
        })
      return state
    } catch (e) {
      if (active === task) cancel()
      throw e
    } finally {
      if (active === task) active = undefined
      progress.value = ''
    }
  }
  window.addEventListener('pagehide', cancel)
  onScopeDispose(() => {
    cancel()
    window.removeEventListener('pagehide', cancel)
  })
  onDeactivated(cancel)
  return { run, cancel, progress }
}
