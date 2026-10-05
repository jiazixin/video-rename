import { onUnmounted, reactive, watch } from 'vue'
import { api } from '../../app/api'

export interface Attempt {
  number: number
  model_config_title: string
  model: string
  fallback: boolean
  started: string
  duration_ms: number
  status: string
  message: string
}

export interface RunItem {
  id: number
  entry: number
  status: string
  original_path: string
  current_path: string
  message: string
  updated_ms?: number
  attempts?: Attempt[]
}

interface RecordPage {
  items: RunItem[]
  total: number
  page: number
}

export function useRunRecords(
  runID: () => number,
  enabled: () => boolean,
  active: () => boolean,
  view: 'logs' | 'failures'
) {
  const state = reactive({ items: [] as RunItem[], total: 0, page: 1, busy: false, error: '' })
  let timer: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined
  let sequence = 0
  let alive = true

  function cancel() {
    clearTimeout(timer)
    controller?.abort()
    sequence++
    state.busy = false
  }

  function schedule() {
    if (!alive || !enabled() || !active()) return
    timer = setTimeout(() => {
      if (document.visibilityState === 'visible') void refresh()
      else schedule()
    }, 3000)
  }

  async function refresh(requested = state.page) {
    if (!alive || !enabled()) return
    cancel()
    const request = ++sequence
    const id = runID()
    const abort = new AbortController()
    controller = abort
    state.busy = true
    try {
      const page = await api<RecordPage>(
        `/runs/${id}/items?view=${view}&page=${requested}`,
        'GET',
        undefined,
        abort.signal
      )
      if (!alive || request !== sequence || id !== runID() || !enabled()) return
      state.items = page.items
      state.total = page.total
      state.page = page.page
      state.error = ''
    } catch (error) {
      if (!abort.signal.aborted && alive && request === sequence)
        state.error = (error as Error).message
    } finally {
      if (alive && request === sequence) {
        state.busy = false
        schedule()
      }
    }
  }

  watch(
    [runID, enabled, active],
    ([id, open], previous) => {
      cancel()
      if (previous && id !== previous[0]) {
        state.items = []
        state.total = 0
        state.page = 1
        state.error = ''
      }
      if (open) void refresh()
    },
    { immediate: true }
  )
  onUnmounted(() => {
    alive = false
    cancel()
  })
  return { state, refresh }
}
