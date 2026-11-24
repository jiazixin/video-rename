import { cleanup, fireEvent, render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import VideoPlayer from './VideoPlayer.vue'

function getVideoElement(): HTMLVideoElement {
  const element = screen.getByLabelText('视频画面')
  if (!(element instanceof HTMLVideoElement)) throw new Error('expected HTMLVideoElement')
  return element
}

async function setRangeValue(slider: HTMLElement, value: string) {
  if (!(slider instanceof HTMLInputElement)) throw new Error('expected HTMLInputElement')
  slider.value = value
  // Range input changes are dispatched via input events because user-event does not drag sliders.
  await fireEvent.input(slider)
}

describe('VideoPlayer', () => {
  beforeEach(() => {
    window.localStorage.clear()
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    // Mirror HTMLMediaElement load() resetting playbackRate to defaultPlaybackRate.
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(function (
      this: HTMLMediaElement
    ) {
      this.playbackRate = this.defaultPlaybackRate
    })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    window.localStorage.clear()
  })

  it('preserves playback speed, volume, and mute state when switching videos via src change', async () => {
    const user = userEvent.setup()
    const view = render(VideoPlayer, {
      props: { src: '/api/entries/1/video' }
    })

    const video = getVideoElement()
    await user.click(screen.getByRole('button', { name: '播放速度 1×' }))
    await user.click(screen.getByRole('menuitemradio', { name: '1.5×' }))
    expect(video.playbackRate).toBe(1.5)
    expect(video.defaultPlaybackRate).toBe(1.5)

    const volumeSlider = screen.getByRole('slider', { name: '音量' })
    await setRangeValue(volumeSlider, '0.4')
    expect(video.volume).toBeCloseTo(0.4)
    expect(video.muted).toBe(false)

    // Switch to the next video and simulate browser resource reload.
    await view.rerender({ src: '/api/entries/2/video' })
    video.load()
    await fireEvent(video, new Event('ratechange'))
    await fireEvent(video, new Event('loadedmetadata'))

    expect(video.playbackRate).toBe(1.5)
    expect(video.defaultPlaybackRate).toBe(1.5)
    expect(video.volume).toBeCloseTo(0.4)
    expect(video.muted).toBe(false)
    expect(screen.getByRole('button', { name: '播放速度 1.5×' })).toBeInTheDocument()
    expect(volumeSlider).toHaveValue('0.4')

    // Mute and switch again: mute stays active and unmuting restores the saved volume.
    await user.click(screen.getByRole('button', { name: '静音' }))
    expect(video.muted).toBe(true)

    await view.rerender({ src: '/api/entries/3/video' })
    video.load()
    await fireEvent(video, new Event('loadedmetadata'))

    expect(video.muted).toBe(true)
    expect(video.volume).toBeCloseTo(0.4)
    expect(screen.getByRole('button', { name: '取消静音' })).toBeInTheDocument()
    expect(volumeSlider).toHaveValue('0')

    await user.click(screen.getByRole('button', { name: '取消静音' }))
    expect(video.muted).toBe(false)
    expect(video.volume).toBeCloseTo(0.4)
    expect(volumeSlider).toHaveValue('0.4')
  })

  it('preserves playback speed, volume, and mute state when remounting for another video', async () => {
    const user = userEvent.setup()
    const first = render(VideoPlayer, {
      props: { src: '/api/entries/10/video' }
    })

    await user.click(screen.getByRole('button', { name: '播放速度 1×' }))
    await user.click(screen.getByRole('menuitemradio', { name: '2×' }))
    await setRangeValue(screen.getByRole('slider', { name: '音量' }), '0.35')
    await user.click(screen.getByRole('button', { name: '静音' }))
    first.unmount()

    render(VideoPlayer, {
      props: { src: '/api/entries/11/video' }
    })
    const nextVideo = getVideoElement()
    await fireEvent(nextVideo, new Event('loadedmetadata'))

    expect(nextVideo.playbackRate).toBe(2)
    expect(nextVideo.defaultPlaybackRate).toBe(2)
    expect(nextVideo.volume).toBeCloseTo(0.35)
    expect(nextVideo.muted).toBe(true)
    expect(screen.getByRole('button', { name: '播放速度 2×' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '取消静音' })).toBeInTheDocument()
    expect(screen.getByRole('slider', { name: '音量' })).toHaveValue('0')
  })

  it('recovers saved playback speed if the browser resets playbackRate while loading a new source', async () => {
    const user = userEvent.setup()
    const view = render(VideoPlayer, {
      props: { src: '/api/entries/20/video' }
    })

    const video = getVideoElement()
    await user.click(screen.getByRole('button', { name: '播放速度 1×' }))
    await user.click(screen.getByRole('menuitemradio', { name: '1.25×' }))

    await view.rerender({ src: '/api/entries/21/video' })
    // Simulate a browser resetting playbackRate to 1 before metadata arrives.
    video.playbackRate = 1
    await fireEvent(video, new Event('ratechange'))
    await fireEvent(video, new Event('loadedmetadata'))

    expect(video.playbackRate).toBe(1.25)
    expect(video.defaultPlaybackRate).toBe(1.25)
    expect(screen.getByRole('button', { name: '播放速度 1.25×' })).toBeInTheDocument()
  })

  it('clears playback error when switching to another video source', async () => {
    const view = render(VideoPlayer, {
      props: { src: '/api/entries/30/video' }
    })

    const video = getVideoElement()
    await fireEvent(video, new Event('error'))
    expect(screen.getByText('无法播放此视频，文件可能不可用或浏览器不支持其编码。')).toBeVisible()

    await view.rerender({ src: '/api/entries/31/video' })
    expect(
      screen.queryByText('无法播放此视频，文件可能不可用或浏览器不支持其编码。')
    ).not.toBeInTheDocument()
  })
})
