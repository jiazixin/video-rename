import { ref, onBeforeUnmount, onDeactivated } from 'vue'
import { api, ApiError, key, session } from '../../app/api'

export type NumberingTask = {
  key: string
  status: string
  phase: string
  path: string
  directories: number
  files: number
  done: number
  total: number
  error: string
  processing_log: string
}
export function useNumberingTask() {
  const progress = ref('')
  let active: { key: string; controller: AbortController } | undefined
  function cancel() {
    if (!active) return
    const task = active
    active = undefined
    task.controller.abort()
    void fetch(`/api/numbering/jobs/${task.key}/cancel`, {
      method: 'POST',
      credentials: 'same-origin',
      keepalive: true,
      headers: {
        'Content-Type': 'application/json',
        ...(session.value ? { 'X-CSRF-Token': session.value.csrf } : {})
      },
      body: '{}'
    }).catch(() => {})
  }
  async function run(
    parent: number,
    action: 'preview' | 'apply',
    batches?: string[],
    preview?: string
  ) {
    const task = { key: key(), controller: new AbortController() }
    active = task
    const signal = task.controller.signal
    progress.value = action === 'preview' ? '正在扫描目录并生成预览…' : '正在核对文件并编号…'
    try {
      let state = await api<NumberingTask>(
        '/numbering/jobs',
        'POST',
        { key: task.key, parent, action, batches, preview },
        signal
      )
      while (state.status === 'running' || state.status === 'cancelling') {
        progress.value =
          action === 'preview'
            ? `正在生成预览：已扫描 ${state.directories} 个目录、${state.files} 个文件${state.path ? ' · ' + state.path : ''}`
            : `正在编号：已完成 ${state.done} / ${state.total} 个目录、${state.files} 个文件${state.path ? ' · ' + state.path : ''}`
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
        state = await api<NumberingTask>(`/numbering/jobs/${task.key}`, 'GET', undefined, signal)
      }
      if (state.status !== 'completed')
        throw new ApiError(
          `${state.error || '编号任务已停止'}${action === 'apply' ? `。已完成 ${state.done} 个目录、${state.files} 个文件；其余目录未继续执行。` : ''}`,
          state.processing_log
        )
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
  onBeforeUnmount(() => {
    cancel()
    window.removeEventListener('pagehide', cancel)
  })
  onDeactivated(cancel)
  return { run, cancel, progress }
}
