import { errorText } from '../../app/errors'
import { computed, ref } from 'vue'
import { api } from '../../app/api'
import type { Entry, Page } from '../../app/contracts'
import { isVideo, isAudio, isImage } from './entry-types'

type MediaQuery = { directory: number; search: string; status: string; sort: string }
type ListQuery = { q: string; status: string; sort: string }

// The same-folder list behind the players' counters, the image strip and the audio list.
// Switching keeps its own lookups, so a slow or capped list never blocks previous/next.
export type MediaQueue = { items: Entry[]; complete: boolean }
const queueLimit = 1000

function useMediaQueue(
  kind: 'video' | 'audio' | 'image',
  match: (e: Entry) => boolean,
  filters: () => ListQuery & { directory: number }
) {
  // undefined while loading or closed; null when the list could not be read.
  const queue = ref<MediaQueue | null>()
  let token = 0
  async function load() {
    const request = ++token,
      { directory, ...query } = filters(),
      items: Entry[] = []
    queue.value = undefined
    let offset = 0,
      generation = '0'
    try {
      while (items.length < queueLimit) {
        const params = new URLSearchParams({
          offset: String(offset),
          limit: '200',
          kind,
          generation,
          ...query
        })
        const result = await api<Page>(`/directories/${directory}/entries?${params}`)
        if (request !== token) return
        items.push(...result.items.filter((e) => e.kind === 'file' && match(e)))
        if (!result.next) {
          queue.value = { items, complete: true }
          return
        }
        offset = result.next
        generation = result.generation
      }
      queue.value = { items: items.slice(0, queueLimit), complete: false }
    } catch {
      // The list is an extra; without it the players still switch through their own lookups.
      if (request === token) queue.value = null
    }
  }
  function clear() {
    token++
    queue.value = undefined
  }
  return { queue, load, clear }
}

// "3 / 12", or "3 / 1000+" when the folder holds more than the list loads.
function position(queue: MediaQueue | null | undefined, current: Entry | undefined) {
  if (!current || !queue) return ''
  const index = queue.items.findIndex((e) => e.id === current.id)
  return index < 0 ? '' : `${index + 1} / ${queue.items.length}${queue.complete ? '' : '+'}`
}

export function useMediaPreview(query: () => MediaQuery) {
  const playing = ref<Entry>()
  const preview = ref<Entry>(),
    imageBusy = ref(false),
    galleryError = ref(''),
    imageZoom = ref(1),
    imageTurn = ref(0)
  // Image switching walks the whole folder by name and ignores filters, so the strip does too.
  const images = useMediaQueue('image', isImage, () => ({
    directory: query().directory,
    q: '',
    status: '',
    sort: query().sort === 'name_desc' ? 'name_desc' : 'name_asc'
  }))
  const imagePosition = computed(() => position(images.queue.value, preview.value))
  // Up to four neighbours on each side of the current image.
  const imageStrip = computed(() => {
    const items = images.queue.value?.items ?? [],
      index = items.findIndex((e) => e.id === preview.value?.id)
    return index < 0 ? [] : items.slice(Math.max(0, index - 4), index + 5)
  })
  let imageSequence = 0
  function closeImage() {
    imageSequence++
    images.clear()
    preview.value = undefined
    imageBusy.value = false
    galleryError.value = ''
  }
  function showImage(e: Entry) {
    imageSequence++
    preview.value = e
    imageBusy.value = false
    galleryError.value = ''
    imageZoom.value = 1
    imageTurn.value = 0
  }
  function openImage(e: Entry) {
    showImage(e)
    void images.load()
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
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
      if (request === imageSequence && preview.value?.id === id) showImage(next)
    } catch (e) {
      if (request === imageSequence) galleryError.value = errorText(e)
    } finally {
      if (request === imageSequence) imageBusy.value = false
    }
  }
  function imageKeys(e: KeyboardEvent) {
    if (!preview.value || e.altKey || e.ctrlKey || e.metaKey) return
    if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(e.key)) {
      e.preventDefault()
      void switchImage(e.key === 'ArrowLeft' || e.key === 'ArrowUp')
    }
  }
  const videoBusy = ref(false),
    videoNotice = ref('')
  const audioPlaying = ref<Entry>(),
    audioNotice = ref(''),
    audioBusy = ref(false)
  const listFilters = () => ({
    directory: query().directory,
    q: query().search,
    status: query().status,
    sort: query().sort
  })
  const videos = useMediaQueue('video', isVideo, listFilters),
    audios = useMediaQueue('audio', isAudio, listFilters)
  const videoPosition = computed(() => position(videos.queue.value, playing.value))
  const audioPosition = computed(() => position(audios.queue.value, audioPlaying.value))
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
    match: isVideo,
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
    audios.clear()
  }
  function closePlayback() {
    closeMediaList(videoList)
    videos.clear()
    closeAudio()
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
      for (;;) {
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
        // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- the compared state can change while awaiting
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
      if (request === list.token.value) list.notice.value = errorText(e)
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
    void videos.load()
  }
  function playAudio(e: Entry) {
    closeMediaList(audioList)
    audioPlaying.value = e
    void audios.load()
  }
  // Picking from the list cancels any switch still in flight and keeps the loaded list.
  function selectAudio(e: Entry) {
    closeMediaList(audioList)
    audioPlaying.value = e
  }

  return {
    preview,
    imageBusy,
    galleryError,
    imageZoom,
    imageTurn,
    imagePosition,
    imageStrip,
    playing,
    videoBusy,
    videoNotice,
    videoPosition,
    audioPlaying,
    audioBusy,
    audioNotice,
    audioPosition,
    audioQueue: audios.queue,
    closeImage,
    showImage,
    openImage,
    switchImage,
    imageKeys,
    closePlayback,
    closeAudio,
    switchVideo,
    switchAudio,
    play,
    playAudio,
    selectAudio
  }
}
