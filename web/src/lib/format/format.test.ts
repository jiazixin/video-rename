import { describe, expect, it } from 'vitest'
import { formatKiB, formatSize } from './bytes'
import { formatPlaybackTime, formatTime } from './date'
import { formatCompact, formatCount, formatPercent, formatThousands } from './number'
import { displayPath } from './path'

describe('format', () => {
  it('formats counts and compact counts', () => {
    expect(formatCount(12345)).toBe('12,345')
    expect(formatCompact(999)).toBe('999')
    expect(formatCompact(12.34)).toBe('12.3')
    expect(formatCompact(12.34, 0)).toBe('12')
    expect(formatCompact(1250)).toBe('1.3K')
    expect(formatCompact(1_000_000)).toBe('1M')
    expect(formatCompact(2_345_678)).toBe('2.35M')
    expect(formatThousands(15200)).toBe('15.20K')
  })

  it('formats percentages with an empty fallback', () => {
    expect(formatPercent(1, 4)).toBe('25.0%')
    expect(formatPercent(1, 0)).toBe('—')
    expect(formatPercent(1, 0, '0.0%')).toBe('0.0%')
  })

  it('formats sizes in binary units', () => {
    expect(formatSize(512)).toBe('512 B')
    expect(formatSize(1536)).toBe('1.5 KiB')
    expect(formatSize(5 * 1048576)).toBe('5.0 MiB')
    expect(formatSize(3 * 1073741824)).toBe('3.0 GiB')
    expect(formatKiB(1048576)).toBe('1024.0 KiB')
  })

  it('formats times and playback positions', () => {
    expect(formatTime('')).toBe('—')
    expect(formatTime(undefined)).toBe('—')
    expect(formatPlaybackTime(65)).toBe('01:05')
    expect(formatPlaybackTime(3725)).toBe('1:02:05')
  })

  it('maps the root and missing directory markers', () => {
    expect(displayPath('/')).toBe('整个媒体库')
    expect(displayPath('/?')).toBe('目录已缺失')
    expect(displayPath('a / b')).toBe('a / b')
  })
})
