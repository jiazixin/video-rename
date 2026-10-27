/** A file size in binary units (KiB, MiB, GiB). */
export function formatSize(bytes: number): string {
  if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GiB`
  if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MiB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${String(bytes)} B`
}

/** A size always in KiB with one decimal, for request diagnostics. */
export function formatKiB(bytes: number): string {
  return `${(bytes / 1024).toFixed(1)} KiB`
}
