import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { readVideoPreferences, writeVideoPreferences } from './video-preferences'

describe('video-preferences', () => {
  beforeEach(() => {
    window.localStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    window.localStorage.clear()
  })

  it('returns default preferences when nothing is stored', () => {
    expect(readVideoPreferences()).toEqual({ speed: 1, volume: 1, muted: false })
  })

  it('persists and normalizes speed, volume, and muted state', () => {
    writeVideoPreferences({ speed: 1.5, volume: 0.35, muted: true })
    expect(readVideoPreferences()).toEqual({ speed: 1.5, volume: 0.35, muted: true })

    writeVideoPreferences({ speed: 99, volume: 2.5, muted: false })
    expect(readVideoPreferences()).toEqual({ speed: 1, volume: 1, muted: false })

    writeVideoPreferences({ speed: 0.75, volume: -0.5, muted: false })
    expect(readVideoPreferences()).toEqual({ speed: 0.75, volume: 0, muted: false })
  })

  it('falls back to defaults when stored JSON is malformed', () => {
    window.localStorage.setItem('video-rename:video-playback-preferences', '{invalid-json')
    expect(readVideoPreferences()).toEqual({ speed: 1, volume: 1, muted: false })
  })

  it('uses in-memory fallback when localStorage throws', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError')
    })
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError')
    })

    writeVideoPreferences({ speed: 2, volume: 0.25, muted: true })
    expect(readVideoPreferences()).toEqual({ speed: 2, volume: 0.25, muted: true })
  })
})
