export const time = (v: string) =>
  v ? new Date(v).toLocaleString('zh-CN', { hour12: false }) : '—'
export const clockPart = (v: string) => new Date(v).toLocaleTimeString('zh-CN', { hour12: false })
