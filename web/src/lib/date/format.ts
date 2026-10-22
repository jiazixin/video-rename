export const formatTime = (v: string) =>
  v ? new Date(v).toLocaleString('zh-CN', { hour12: false }) : '—'
export const clockPart = (v: string) => new Date(v).toLocaleTimeString('zh-CN', { hour12: false })
/** Media position in seconds as mm:ss, with hours when needed (h:mm:ss). */
export function formatPlaybackTime(seconds: number) {
  const s = Math.floor(seconds || 0)
  const hours = Math.floor(s / 3600)
  const minutes = String(Math.floor(s / 60) % 60).padStart(2, '0')
  return `${hours ? `${hours}:` : ''}${minutes}:${String(s % 60).padStart(2, '0')}`
}
