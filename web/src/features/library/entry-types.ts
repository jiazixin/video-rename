import type { Entry } from '../../app/contracts'

export const video = (e: Entry) =>
  /\.(mp4|m4v|mkv|mov|avi|webm|flv|wmv|mpg|mpeg|ts|m2ts|mts|3gp|ogv|rmvb|asf|vob)$/i.test(e.name)
export const isImage = (e: Entry) =>
  e.kind === 'file' && /\.(jpe?g|png|gif|webp|avif|bmp)$/i.test(e.name)
export const isAudio = (e: Entry) =>
  e.kind === 'file' &&
  /\.(mp3|m4a|m4b|aac|flac|wav|wave|ogg|oga|opus|wma|aiff|aif|caf)$/i.test(e.name)
export const markable = (e: Entry) =>
  e.kind === 'directory' || (e.kind === 'file' && (video(e) || isImage(e) || isAudio(e)))
export const protectedMedia = (e: Entry) => (isImage(e) || isAudio(e)) && e.processed === 'marked'
export const marked = (e: Entry) => (isImage(e) || isAudio(e) ? protectedMedia(e) : !!e.processed)
export function normalizedExtension(name: string) {
  const ext = name.match(/\.[^.]+$/)?.[0]?.toLowerCase() || ''
  return ext === '.jpeg' ? '.jpg' : ext
}
export const selectable = (e: Entry) => e.kind === 'file' || e.kind === 'directory'
