import { inject, nextTick, onActivated, onDeactivated, onScopeDispose, readonly, ref } from 'vue'
import { matchedRouteKey, onBeforeRouteLeave } from 'vue-router'
import type { ToastItem } from '../components/toast-types'

/** How long a notice stays before it closes itself, in milliseconds. */
const toastDurationMs = 5000
const maxNotices = 5

const items = ref<ToastItem[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextID = 0

/** The notices on screen, newest first; rendered once by ToastNotice. */
export const toasts = readonly(items)

const animated = ref(true)

/** False while notices of a page being left are removed without the fade-out. */
export const toastsAnimated = readonly(animated)

/** Closes notice id before its timer ends. */
export function closeToast(id: number): void {
  clearTimeout(timers.get(id))
  timers.delete(id)
  items.value = items.value.filter((item) => item.id !== id)
}

function pushToast(message: string, tone: ToastItem['tone']): number {
  while (items.value.length >= maxNotices) {
    const oldest = items.value.at(-1)
    if (oldest) closeToast(oldest.id)
  }
  const id = ++nextID
  items.value.unshift({ id, message, tone })
  timers.set(
    id,
    setTimeout(() => {
      closeToast(id)
    }, toastDurationMs)
  )
  return id
}

/**
 * Returns the notice functions of the calling page, which must render inside
 * RouterView. Leaving the route, KeepAlive hiding the page or unmounting it
 * drops its notices and ignores late results, so a slow request never reports
 * on another page. The leaving page fades out first, so its notices go when the
 * navigation starts rather than at unmount.
 */
export function useToast(): {
  show: (message: string, tone?: ToastItem['tone']) => void
  clear: () => void
} {
  let isActive = true
  const own = new Set<number>()
  function show(message: string, tone: ToastItem['tone'] = 'success') {
    if (!message || !isActive) return
    const visible = new Set(items.value.map((item) => item.id))
    for (const id of own) if (!visible.has(id)) own.delete(id)
    own.add(pushToast(message, tone))
  }
  function clear() {
    for (const id of own) closeToast(id)
    own.clear()
  }
  function deactivate() {
    isActive = false
    if (!own.size) return
    // The page is going away; its notices leave at once instead of fading out.
    animated.value = false
    clear()
    void nextTick(() => {
      animated.value = true
    })
  }
  onActivated(() => {
    isActive = true
  })
  if (inject(matchedRouteKey, undefined)) onBeforeRouteLeave(deactivate)
  onDeactivated(deactivate)
  onScopeDispose(deactivate)
  return { show, clear }
}
