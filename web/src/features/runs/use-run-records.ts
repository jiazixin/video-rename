import { onUnmounted, reactive, ref, watch } from 'vue'
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
  directory: string
  original_name: string
  new_name: string
  operation_status: string
  undone: boolean
  message: string
  updated_ms?: number
  attempts: Attempt[]
}
export interface RunDirectory {
  path: string
  total: number
  done: number
  failed: number
  running: number
  queued: number
  skipped: number
  cancelled: number
}
interface RecordPage {
  items: RunItem[]
  directories: RunDirectory[]
  total: number
  all_total: number
  failed_total: number
  page: number
}

export function useRunRecords(runID: () => number, enabled: () => boolean, active: () => boolean) {
  const filter = ref<'all' | 'failed'>('all')
  const directory = ref('all')
  const state = reactive({
    items: [] as RunItem[],
    directories: [] as RunDirectory[],
    total: 0,
    all_total: 0,
    failed_total: 0,
    page: 1,
    busy: false,
    loaded: false,
    error: ''
  })
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
    if (!enabled()) return
    cancel()
    const request = ++sequence
    const id = runID()
    const abort = new AbortController()
    controller = abort
    state.busy = true
    const query = new URLSearchParams({ page: String(requested) })
    if (filter.value === 'failed') query.set('status', 'failed')
    if (directory.value !== 'all') query.set('directory', directory.value.slice(5))
    try {
      const page = await api<RecordPage>(
        `/runs/${id}/items?${query}`,
        'GET',
        undefined,
        abort.signal
      )
      if (!alive || request !== sequence || id !== runID() || !enabled()) return
      Object.assign(state, page, { loaded: true, error: '' })
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
  watch([filter, directory], () => {
    cancel()
    state.items = []
    state.page = 1
    state.total = 0
    state.loaded = false
    state.error = ''
    if (enabled()) void refresh()
  })
  watch(
    [runID, enabled, active],
    ([id, open], previous) => {
      cancel()
      if (previous && id !== previous[0]) {
        state.items = []
        state.directories = []
        state.page = 1
        state.loaded = false
      }
      if (open) void refresh()
    },
    { immediate: true }
  )
  onUnmounted(() => {
    alive = false
    cancel()
  })
  return { state, refresh, filter, directory }
}
