/** A count with Chinese digit grouping, such as 12,345. */
export function formatCount(value: number): string {
  return value.toLocaleString('zh-CN')
}

/**
 * A short count for charts and token totals: 1.2K, 3.45M. Values below 1000
 * keep smallDigits decimals.
 */
export function formatCompact(value: number, smallDigits = 1): string {
  if (value >= 999_950) return `${(value / 1e6).toFixed(2).replace(/\.?0+$/, '')}M`
  if (value >= 1000) return `${(value / 1000).toFixed(1).replace(/\.?0+$/, '')}K`
  const scale = 10 ** smallDigits
  return String(Math.round(value * scale) / scale)
}

/** Thousands with two decimals for diagnostics, such as 15.20K. */
export function formatThousands(value: number): string {
  return `${(value / 1000).toFixed(2)}K`
}

/** part / whole as a percentage with one decimal, or empty when whole is 0. */
export function formatPercent(part: number, whole: number, empty = '—'): string {
  return whole ? `${((part / whole) * 100).toFixed(1)}%` : empty
}

/** A decimal number with at most maximumDigits fraction digits. */
export function formatDecimal(value: number, maximumDigits: number): string {
  return value.toLocaleString('zh-CN', { maximumFractionDigits: maximumDigits })
}
