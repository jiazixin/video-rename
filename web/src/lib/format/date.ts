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
