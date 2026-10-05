import { computed, onUnmounted, ref } from 'vue'
import { api } from '../../app/api'
import {
  type LibraryScanSettings,
  type LibraryScanState,
  type LibraryScanStatus
} from '../../app/contracts'

// Poll only while the owning page and browser tab are visible. GET is an index
// status request; it must never trigger a filesystem scan.
export function useLibraryScan(onPoll?: (signal: AbortSignal) => Promise<void>) {
  const state = ref<LibraryScanState>(),
    error = ref(''),
    actionError = ref(''),
    submitting = ref(false)
  const scanning = computed(() =>
    ['scanning', 'cancelling'].includes(state.value?.status.status || '')
  )
  let active = false,
    serial = 0,
    timer: ReturnType<typeof setTimeout> | undefined,
    controller: AbortController | undefined

  async function refresh() {
    clearTimeout(timer)
    controller?.abort()
    if (!active || document.hidden) return
    const request = ++serial,
      current = new AbortController()
    controller = current
    try {
      const result = await api<LibraryScanState>('/library/scan', 'GET', undefined, current.signal)
      if (request !== serial || !active || current.signal.aborted) return
      state.value = result
      error.value = ''
    } catch (e) {
      if (request === serial && !current.signal.aborted)
        error.value = `扫描状态读取失败：${(e as Error).message}`
    }
    if (request === serial && active && !current.signal.aborted) {
      await onPoll?.(current.signal)
      if (request === serial && active && !document.hidden)
        timer = setTimeout(() => void refresh(), 5000)
    }
  }
  function visibilityChanged() {
    if (document.hidden) {
      serial++
      clearTimeout(timer)
      controller?.abort()
    } else void refresh()
  }
  function startPolling() {
    if (active) return
    active = true
    document.addEventListener('visibilitychange', visibilityChanged)
    void refresh()
  }
  function stopPolling() {
    active = false
    serial++
    clearTimeout(timer)
    controller?.abort()
    document.removeEventListener('visibilitychange', visibilityChanged)
  }
  async function action(path: string, body: unknown) {
    if (submitting.value) return
    submitting.value = true
    actionError.value = ''
    try {
      const status = await api<LibraryScanStatus>(path, 'POST', body)
      if (state.value) state.value.status = status
      void refresh()
      return status
    } catch (e) {
      actionError.value = (e as Error).message
    } finally {
      submitting.value = false
    }
  }
  async function save(settings: LibraryScanSettings) {
    if (submitting.value) return
    submitting.value = true
    actionError.value = ''
    try {
      const saved = await api<LibraryScanSettings>('/library/scan/settings', 'PUT', settings)
      if (state.value) state.value.settings = saved
      void refresh()
      return saved
    } catch (e) {
      actionError.value = (e as Error).message
    } finally {
      submitting.value = false
    }
  }
  onUnmounted(stopPolling)
  return {
    state,
    error,
    actionError,
    submitting,
    scanning,
    refresh,
    startPolling,
    stopPolling,
    start: (targets?: number[]) => action('/library/scan', targets ? { targets } : {}),
    cancel: () => action('/library/scan/cancel', {}),
    save
  }
}
