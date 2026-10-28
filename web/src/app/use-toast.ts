import { nextTick, onActivated, onDeactivated, onScopeDispose, readonly, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'

/** One write-operation result shown in the global notice stack. */
export type ToastItem = { id: number; message: string }

/** How long a notice stays before it closes itself, in milliseconds. */
const toastDurationMs = 5000

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

function pushToast(message: string): number {
  const id = ++nextID
  items.value.unshift({ id, message })
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
export function useToast(): { show: (message: string) => void; clear: () => void } {
  let isActive = true
  const own = new Set<number>()
  function show(message: string) {
    if (!message || !isActive) return
    own.add(pushToast(message))
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
  onBeforeRouteLeave(deactivate)
  onDeactivated(deactivate)
  onScopeDispose(deactivate)
  return { show, clear }
}
