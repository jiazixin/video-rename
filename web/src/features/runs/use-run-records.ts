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
export interface LiveStage {
  stage: 'extracting' | 'model_wait' | 'model' | 'retry_wait' | 'committing'
  since_ms: number
  attempt: number
  fallback: boolean
  frames: number
  title: string
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
  stage?: LiveStage
}
export interface RecentItem {
  id: number
  status: string
  message: string
  directory: string
  original_name: string
  new_name: string
  operation_status: string
  undone: boolean
  started_ms: number
  finished_ms: number
  attempts: number
}
export interface SkipReason {
  message: string
  count: number
}
export type RecordFilter = 'all' | 'running' | 'done' | 'skipped' | 'failed'
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
  done_total: number
  running_total: number
  skipped_total: number
  failed_total: number
  page: number
  recent?: RecentItem[]
  skip_reasons?: SkipReason[]
}

export function useRunRecords(runID: () => number, enabled: () => boolean, active: () => boolean) {
  // An active batch opens on the files being processed right now.
  const filter = ref<RecordFilter>(active() ? 'running' : 'all')
  const directory = ref('all')
  const state = reactive({
    items: [] as RunItem[],
    directories: [] as RunDirectory[],
    total: 0,
    all_total: 0,
    done_total: 0,
    running_total: 0,
    skipped_total: 0,
    failed_total: 0,
    recent: [] as RecentItem[],
    skip_reasons: [] as SkipReason[],
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
    if (filter.value !== 'all') query.set('status', filter.value)
    if (directory.value !== 'all') query.set('directory', directory.value.slice(5))
    try {
      const page = await api<RecordPage>(
        `/runs/${id}/items?${query}`,
        'GET',
        undefined,
        abort.signal
      )
      if (!alive || request !== sequence || id !== runID() || !enabled()) return
      Object.assign(state, { recent: [], skip_reasons: [] }, page, { loaded: true, error: '' })
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
    state.recent = []
    state.skip_reasons = []
    state.loaded = false
    state.error = ''
    if (enabled()) void refresh()
  })
  watch(
    [runID, enabled, active],
    ([id, open], previous) => {
      cancel()
      if (previous && id !== previous[0]) {
        filter.value = active() ? 'running' : 'all'
        state.items = []
        state.recent = []
        state.skip_reasons = []
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
