import { computed, ref } from 'vue'
import { statusNames } from '../../app/labels'

export type RunCategory = 'all' | 'manual' | 'automatic'

const statuses = [
  'queued',
  'scanning',
  'running',
  'paused',
  'cancelling',
  'completed',
  'partial_failure',
  'failed',
  'cancelled',
  'interrupted'
]

/** Status filter options in lifecycle order. */
export const statusOptions = statuses.map((value) => ({
  value,
  label: statusNames[value] || value
}))

// Batch list filters and the query string they produce. Relative periods are
// resolved against the current time at each request.
export function useRunFilters() {
  const category = ref<RunCategory>('all'),
    task = ref(''),
    status = ref(''),
    profile = ref(''),
    duration = ref(''),
    period = ref('all'),
    from = ref(''),
    to = ref('')
  const hasFilters = computed(
    () =>
      category.value !== 'all' ||
      !!task.value ||
      !!status.value ||
      !!profile.value ||
      !!duration.value ||
      period.value !== 'all'
  )
  function filterQuery() {
    const q = new URLSearchParams({
      status: status.value,
      profile: profile.value,
      duration: duration.value
    })
    const end = new Date(),
      start = new Date(end)
    if (period.value === 'custom') {
      if (from.value) q.set('from', new Date(from.value).toISOString())
      if (to.value) q.set('to', new Date(to.value).toISOString())
    } else if (period.value !== 'all') {
      if (period.value === 'today') start.setHours(0, 0, 0, 0)
      else start.setTime(end.getTime() - Number(period.value) * 86400000)
      q.set('from', start.toISOString())
      q.set('to', end.toISOString())
    }
    return q.toString()
  }
  function resetFilters() {
    category.value = 'all'
    task.value = ''
    status.value = ''
    profile.value = ''
    duration.value = ''
    period.value = 'all'
    from.value = ''
    to.value = ''
  }
  return {
    category,
    task,
    status,
    profile,
    duration,
    period,
    from,
    to,
    hasFilters,
    filterQuery,
    resetFilters
  }
}
