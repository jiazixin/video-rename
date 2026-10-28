import { onScopeDispose, ref } from 'vue'
import { isActiveRunStatus, type Run } from '../../app/contracts'
import { formatElapsedTime, formatEstimatedTime } from '../../lib/format/date'

/** Files of r that reached a final result. */
export const processed = (r: Run) => (r.done || 0) + (r.skipped || 0) + (r.failed || 0)

/** Share of discovered files with a final result, in whole percent. */
export const progress = (r: Run) =>
  r.discovered > 0 ? Math.min(100, Math.round((processed(r) / r.discovered) * 100)) : 0

// Smoothing weight of the newest throughput sample in the remaining-time estimate.
const ETA_ALPHA = 0.3

function etaWarmup(total: number) {
  if (total <= 2) return Infinity
  return total <= 10 ? Math.max(2, Math.ceil(total / 2)) : 10
}

type EtaState = { base: number; at: number; prev: number; prevAt: number; rate: number }

// Elapsed and remaining time of the listed runs. The clock ticks every second
// while a started run is active, so elapsed times advance between polls.
export function useRunTiming(runs: () => Run[]) {
  const now = ref(Date.now())
  const etas = ref<Record<number, string>>({})
  const etaState = new Map<number, EtaState>()
  const ticker = setInterval(() => {
    if (runs().some((r) => isActiveRunStatus(r.status) && r.started)) now.value = Date.now()
  }, 1000)
  onScopeDispose(() => {
    clearInterval(ticker)
  })
  function elapsed(r: Run) {
    if (!r.started) return r.status === 'queued' ? '等待开始' : '—'
    const start = Date.parse(r.started)
    let end = NaN
    if (r.finished) end = Date.parse(r.finished)
    else if (isActiveRunStatus(r.status)) end = now.value
    if (!Number.isFinite(start) || !Number.isFinite(end)) return '—'
    return formatElapsedTime((end - start) / 1000)
  }
  /** Updates the estimates from a fresh list; call once per poll. */
  function trackEta(list: Run[]) {
    now.value = Date.now()
    const live = list.filter((r) => r.status === 'running'),
      next: Record<number, string> = {}
    for (const r of live) {
      const total = r.discovered || 0,
        p = processed(r),
        t = Date.now()
      let s = etaState.get(r.id)
      if (!s) {
        s = { base: p, at: t, prev: p, prevAt: t, rate: 0 }
        etaState.set(r.id, s)
      } else {
        const items = p - s.prev,
          span = (t - s.prevAt) / 1000
        if (items > 0 && span > 0) {
          const instant = items / span
          s.rate = s.rate > 0 ? ETA_ALPHA * instant + (1 - ETA_ALPHA) * s.rate : instant
        }
        s.prev = p
        s.prevAt = t
      }
      const runFor = (t - s.at) / 1000,
        remaining = Math.max(total - p, 0),
        warm = etaWarmup(total)
      let rate = s.rate
      if (!(rate > 0) && p - s.base > 0) rate = (p - s.base) / runFor
      if (total > 0 && p - s.base >= warm && runFor >= 5 && rate > 0)
        next[r.id] = remaining > 0 ? formatEstimatedTime(remaining / rate) : '<1s'
      else next[r.id] = '预计时间计算中…'
    }
    for (const id of [...etaState.keys()]) if (!live.some((r) => r.id === id)) etaState.delete(id)
    etas.value = next
  }
  return { etas, elapsed, trackEta }
}
