import { ref, computed } from 'vue'
import type { Entry } from '../../app/contracts'
import { isVideo, selectable, markable } from './entry-types'

export function useLibrarySelection(rows: () => Entry[]) {
  const multi = ref(false),
    chosen = ref(new Map<number, Entry>())
  const autoEntries = computed(() =>
    [...chosen.value.values()].filter(
      (e) => (e.kind === 'directory' || isVideo(e)) && !e.processed && !e.task_status
    )
  )
  const extractEntries = computed(() =>
    [...chosen.value.values()].filter(
      (e) => (e.kind === 'directory' || isVideo(e)) && !e.task_status
    )
  )
  const markEntries = computed(() => [...chosen.value.values()].filter(markable))
  const restoreEntries = computed(() =>
    [...chosen.value.values()].filter((e) => e.restore_id && !e.task_status)
  )
  function toggle(e: Entry) {
    if (chosen.value.has(e.id)) chosen.value.delete(e.id)
    else chosen.value.set(e.id, e)
  }
  let selectionAnchor: number | undefined
  function clearSelection() {
    chosen.value.clear()
    resetSelectionAnchor()
  }
  function selectEntry(e: Entry, event: MouseEvent) {
    const entries = rows()
    const start = entries.findIndex((row) => row.id === selectionAnchor),
      end = entries.findIndex((row) => row.id === e.id)
    if (event.shiftKey && start >= 0 && end >= 0) {
      for (const row of entries.slice(Math.min(start, end), Math.max(start, end) + 1))
        if (selectable(row)) chosen.value.set(row.id, row)
    } else {
      toggle(e)
      selectionAnchor = e.id
    }
    // Shift-click may target an already selected checkbox; keep the native
    // checked state in sync even when the selected IDs did not change.
    ;(event.currentTarget as HTMLInputElement).checked = chosen.value.has(e.id)
  }
  function selectPage(invert = false) {
    resetSelectionAnchor()
    for (const e of rows())
      if (selectable(e)) {
        if (invert) toggle(e)
        else chosen.value.set(e.id, e)
      }
  }
  function resetSelectionAnchor() {
    selectionAnchor = undefined
  }
  return {
    multi,
    chosen,
    autoEntries,
    extractEntries,
    markEntries,
    restoreEntries,
    clearSelection,
    selectEntry,
    selectPage,
    resetSelectionAnchor
  }
}
