import { ref } from 'vue'
import { api } from '../../app/api'
import type { Entry, Page } from '../../app/contracts'
import { video, isAudio } from './entry-types'

type MediaQuery = { directory: number; search: string; status: string; sort: string }

export function useMediaPreview(query: () => MediaQuery) {
  const playing = ref<Entry>()
  const preview = ref<Entry>(),
    imageError = ref(false),
    imageBusy = ref(false),
    galleryError = ref('')
  let imageSequence = 0
  function closeImage() {
    imageSequence++
    preview.value = undefined
    imageBusy.value = false
    galleryError.value = ''
  }
  function showImage(e: Entry) {
    imageSequence++
    preview.value = e
    imageBusy.value = false
    imageError.value = false
    galleryError.value = ''
  }
  async function switchImage(previous: boolean) {
    if (!preview.value || imageBusy.value) return
    const id = preview.value.id,
      request = ++imageSequence
    imageBusy.value = true
    galleryError.value = ''
    try {
      const direction = (query().sort === 'name_desc' ? !previous : previous) ? 'previous' : 'next'
      const next = await api<Entry>(`/entries/${id}/image-neighbor?direction=${direction}`)
      if (request === imageSequence && preview.value?.id === id) showImage(next)
    } catch (e) {
      if (request === imageSequence) galleryError.value = (e as Error).message
    } finally {
      if (request === imageSequence) imageBusy.value = false
    }
  }
  function imageKeys(e: KeyboardEvent) {
    if (!preview.value || e.altKey || e.ctrlKey || e.metaKey) return
    if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(e.key)) {
      e.preventDefault()
      switchImage(e.key === 'ArrowLeft' || e.key === 'ArrowUp')
    }
  }
  const videoBusy = ref(false),
    videoNotice = ref('')
  const audioPlaying = ref<Entry>(),
    audioNotice = ref(''),
    audioBusy = ref(false)
  // Cross-page switching shared by the video and audio players: replay the
  // directory's filter, search and sort to find the current entry's neighbour.
  type MediaList = {
    kind: 'video' | 'audio'
    unit: string
    match: (e: Entry) => boolean
    current: { value: Entry | undefined }
    notice: { value: string }
    busy: { value: boolean }
    token: { value: number }
  }
  const videoList: MediaList = {
    kind: 'video',
    unit: '集',
    match: video,
    current: playing,
    notice: videoNotice,
    busy: videoBusy,
    token: { value: 0 }
  }
  const audioList: MediaList = {
    kind: 'audio',
    unit: '首',
    match: isAudio,
    current: audioPlaying,
    notice: audioNotice,
    busy: audioBusy,
    token: { value: 0 }
  }
  function closeMediaList(list: MediaList) {
    list.token.value++
    list.busy.value = false
    list.current.value = undefined
    list.notice.value = ''
  }
  function closeAudio() {
    closeMediaList(audioList)
  }
  function closePlayback() {
    closeMediaList(videoList)
    closeMediaList(audioList)
  }
  async function switchMedia(previous: boolean, list: MediaList) {
    if (!list.current.value || list.busy.value) return
    const id = list.current.value.id,
      request = ++list.token.value
    list.busy.value = true
    list.notice.value = ''
    try {
      let offset = 0,
        generation = '0',
        last: Entry | undefined,
        found = false
      // Read the same ordered, filtered directory across page boundaries.
      while (true) {
        const params = new URLSearchParams({
          offset: String(offset),
          limit: '200',
          q: query().search,
          kind: list.kind,
          status: query().status,
          sort: query().sort,
          generation
        })
        const result = await api<Page>(`/directories/${query().directory}/entries?${params}`)
        if (request !== list.token.value || list.current.value?.id !== id) return
        for (const entry of result.items) {
          if (entry.kind !== 'file' || !list.match(entry)) continue
          if (found && !previous) {
            list.current.value = entry
            return
          }
          if (entry.id === id) {
            found = true
            if (previous) {
              if (last) list.current.value = last
              else list.notice.value = `已经是第一${list.unit}`
              return
            }
          }
          last = entry
        }
        if (!result.next) {
          list.notice.value = found
            ? `已经是最后一${list.unit}`
            : '当前文件已不在列表中，请关闭后重新选择'
          return
        }
        offset = result.next
        generation = result.generation
      }
    } catch (e) {
      if (request === list.token.value) list.notice.value = (e as Error).message
    } finally {
      if (request === list.token.value) list.busy.value = false
    }
  }
  function switchVideo(previous: boolean) {
    return switchMedia(previous, videoList)
  }
  function switchAudio(previous: boolean) {
    return switchMedia(previous, audioList)
  }
  function play(e: Entry) {
    closeMediaList(videoList)
    playing.value = e
  }
  function playAudio(e: Entry) {
    closeMediaList(audioList)
    audioPlaying.value = e
  }

  return {
    preview,
    imageError,
    imageBusy,
    galleryError,
    playing,
    videoBusy,
    videoNotice,
    audioPlaying,
    audioBusy,
    audioNotice,
    closeImage,
    showImage,
    switchImage,
    imageKeys,
    closePlayback,
    closeAudio,
    switchVideo,
    switchAudio,
    play,
    playAudio
  }
}
