// Fastest on top so the speed menu reads in the same direction it opens.
export const PLAYBACK_SPEEDS: readonly number[] = [2, 1.5, 1.25, 1, 0.75, 0.5]

export type VideoPlaybackPreferences = {
  speed: number
  volume: number
  muted: boolean
}

const STORAGE_KEY = 'video-rename:video-playback-preferences'
const VALID_SPEEDS = new Set<number>(PLAYBACK_SPEEDS)
const DEFAULT_PREFERENCES: VideoPlaybackPreferences = {
  speed: 1,
  volume: 1,
  muted: false
}

let fallbackPreferences: VideoPlaybackPreferences = { ...DEFAULT_PREFERENCES }

function normalizePreferences(raw: unknown): VideoPlaybackPreferences {
  if (typeof raw !== 'object' || raw === null) return { ...DEFAULT_PREFERENCES }
  const record = raw as Record<string, unknown>
  const speed =
    typeof record.speed === 'number' && VALID_SPEEDS.has(record.speed)
      ? record.speed
      : DEFAULT_PREFERENCES.speed
  const volume =
    typeof record.volume === 'number' && Number.isFinite(record.volume)
      ? Math.min(1, Math.max(0, record.volume))
      : DEFAULT_PREFERENCES.volume
  const muted = typeof record.muted === 'boolean' ? record.muted : DEFAULT_PREFERENCES.muted
  return { speed, volume, muted }
}

export function readVideoPreferences(): VideoPlaybackPreferences {
  let raw: string | null
  try {
    raw = window.localStorage.getItem(STORAGE_KEY)
  } catch {
    // Storage access may fail in restricted browser contexts; keep in-memory session preferences.
    return { ...fallbackPreferences }
  }
  if (raw === null) return { ...DEFAULT_PREFERENCES }
  try {
    return normalizePreferences(JSON.parse(raw))
  } catch {
    // Ignore corrupt stored JSON and fall back to default playback preferences.
    return { ...DEFAULT_PREFERENCES }
  }
}

export function writeVideoPreferences(preferences: VideoPlaybackPreferences): void {
  const normalized = normalizePreferences(preferences)
  fallbackPreferences = normalized
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(normalized))
  } catch {
    // Storage writes may fail when quota is full or storage is blocked; in-memory fallback stays active.
  }
}
