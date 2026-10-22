import { ref, type Ref } from 'vue'

interface StickyHeaderElements {
  /** The sticky controls whose height moves the header. */
  fixedControls: Ref<HTMLElement | undefined>
  /** The scrolling table whose column widths the header copies. */
  fileTable: Ref<HTMLTableElement | undefined>
  /** The horizontally scrolling header copy. */
  headerScroll: Ref<HTMLElement | undefined>
}

// Keep the visible header in the sticky controls. Only column geometry changes
// need measuring; vertical scrolling is handled entirely by CSS.
export function useStickyHeader({ fixedControls, fileTable, headerScroll }: StickyHeaderElements) {
  const headerColumns = ref<number[]>([]),
    headerWidth = ref(0)
  let layoutObserver: ResizeObserver | undefined,
    headerFrame = 0
  function updateHeaderLayout() {
    headerFrame = 0
    const table = fileTable.value,
      head = table?.tHead
    if (!table || !head) return
    headerColumns.value = Array.from(
      head.rows[0].cells,
      (cell) => cell.getBoundingClientRect().width
    )
    headerWidth.value = table.getBoundingClientRect().width
    if (headerScroll.value) headerScroll.value.scrollLeft = table.parentElement!.scrollLeft
  }
  function scheduleHeaderLayout() {
    if (!headerFrame) headerFrame = requestAnimationFrame(updateHeaderLayout)
  }
  function syncTableScroll(event: Event) {
    const source = event.currentTarget as HTMLElement
    const target =
      source === headerScroll.value ? fileTable.value?.parentElement : headerScroll.value
    if (target && target.scrollLeft !== source.scrollLeft) target.scrollLeft = source.scrollLeft
  }
  function observeLayout() {
    layoutObserver = new ResizeObserver(scheduleHeaderLayout)
    if (fixedControls.value) layoutObserver.observe(fixedControls.value)
    if (fileTable.value) layoutObserver.observe(fileTable.value)
    scheduleHeaderLayout()
  }
  function stopObservingLayout() {
    layoutObserver?.disconnect()
    layoutObserver = undefined
    cancelAnimationFrame(headerFrame)
    headerFrame = 0
  }
  return {
    headerColumns,
    headerWidth,
    scheduleHeaderLayout,
    syncTableScroll,
    observeLayout,
    stopObservingLayout
  }
}
