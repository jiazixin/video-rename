// All times display in the browser's time zone, in Chinese 24-hour format.
const zh = 'zh-CN'

/** The browser's IANA time zone, such as Asia/Shanghai; sent where the server buckets times. */
export function localTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone
}

/** Date and time, such as 2026/10/26 13:05:09; — when empty. */
export function formatTime(value: string | number | Date | undefined): string {
  if (value === undefined || value === '') return '—'
  return new Date(value).toLocaleString(zh, { hour12: false })
}

/** Time of day, such as 13:05:09. */
export function formatClock(value: string | number | Date): string {
  return new Date(value).toLocaleTimeString(zh, { hour12: false })
}

/** Hour and minute, such as 13:05. */
export function formatHourMinute(value: string | number | Date): string {
  return new Date(value).toLocaleTimeString(zh, {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit'
  })
}

/** Calendar date, such as 2026/10/26. */
export function formatDate(value: string | number | Date): string {
  return new Date(value).toLocaleDateString(zh)
}

/** Month and day, such as 10/26. */
export function formatMonthDay(value: string | number | Date): string {
  return new Date(value).toLocaleDateString(zh, { month: 'numeric', day: 'numeric' })
}

/** Long date, such as 2026年10月26日. */
export function formatLongDate(value: string | number | Date): string {
  return new Date(value).toLocaleDateString(zh, {
    year: 'numeric',
    month: 'long',
    day: 'numeric'
  })
}

/** Media position in seconds as mm:ss, with hours when needed (h:mm:ss). */
export function formatPlaybackTime(seconds: number): string {
  const s = Math.floor(seconds || 0)
  const hours = Math.floor(s / 3600)
  const minutes = String(Math.floor(s / 60) % 60).padStart(2, '0')
  return `${hours ? `${String(hours)}:` : ''}${minutes}:${String(s % 60).padStart(2, '0')}`
}

/** Estimated duration in seconds, rounded to the nearest second. */
export function formatEstimatedTime(seconds: number): string {
  const s = Math.max(0, Math.round(seconds))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s`
  return `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`
}

/** Elapsed duration in seconds, including all units between days and seconds. */
export function formatElapsedTime(value: number): string {
  const seconds = Math.max(0, Math.floor(value))
  const days = Math.floor(seconds / 86400),
    hours = Math.floor((seconds % 86400) / 3600),
    minutes = Math.floor((seconds % 3600) / 60)
  const parts: string[] = []
  if (days) parts.push(`${days}d`)
  if (days || hours) parts.push(`${hours}h`)
  if (days || hours || minutes) parts.push(`${minutes}m`)
  parts.push(`${seconds % 60}s`)
  return parts.join('')
}
