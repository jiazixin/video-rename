/** Rounds value up to 1, 2, 5 or 10 times a power of ten, for chart axes. */
export function niceCeil(value: number): number {
  if (value <= 0) return 1
  const pow = 10 ** Math.floor(Math.log10(value))
  const step = [1, 2, 5].find((s) => value / pow <= s) ?? 10
  return step * pow
}

/**
 * Moves the keyboard-selected point of count points by step (1 or -1). With no
 * selection, the left arrow starts at the last point and the right at the first.
 */
export function stepActive(active: number, count: number, step: 1 | -1): number {
  if (active < 0) return step < 0 ? count - 1 : 0
  return Math.max(0, Math.min(count - 1, active + step))
}
