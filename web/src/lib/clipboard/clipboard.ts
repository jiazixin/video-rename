let copying = false

function copyWithSelection(text: string): void {
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const selection = window.getSelection()
  const ranges = selection
    ? Array.from({ length: selection.rangeCount }, (_, index) =>
        selection.getRangeAt(index).cloneRange()
      )
    : []
  const input =
    previous instanceof HTMLInputElement || previous instanceof HTMLTextAreaElement
      ? previous
      : null
  const inputSelection =
    input && input.selectionStart !== null && input.selectionEnd !== null
      ? {
          start: input.selectionStart,
          end: input.selectionEnd,
          direction: input.selectionDirection
        }
      : null
  const scroll = { x: window.scrollX, y: window.scrollY }
  const ancestors: { element: HTMLElement; left: number; top: number }[] = []
  for (let element = previous; element; element = element.parentElement) {
    ancestors.push({ element, left: element.scrollLeft, top: element.scrollTop })
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.readOnly = true
  textarea.tabIndex = -1
  textarea.style.cssText =
    'position:fixed;top:0;left:0;width:1px;height:1px;padding:0;margin:0;border:0;opacity:0'
  document.body.append(textarea)
  try {
    textarea.focus({ preventScroll: true })
    textarea.select()
    textarea.setSelectionRange(0, text.length)
    if (!document.execCommand('copy')) throw new Error('复制失败')
  } finally {
    textarea.remove()
    if (selection) {
      selection.removeAllRanges()
      for (const range of ranges) {
        if (range.startContainer.isConnected && range.endContainer.isConnected)
          selection.addRange(range)
      }
    }
    // Changing the document selection can also reset a focused input's selection.
    if (previous?.isConnected) previous.focus({ preventScroll: true })
    if (input?.isConnected && inputSelection) {
      input.setSelectionRange(
        inputSelection.start,
        inputSelection.end,
        inputSelection.direction ?? undefined
      )
    }
    for (const { element, left, top } of ancestors) {
      element.scrollLeft = left
      element.scrollTop = top
    }
    window.scrollTo(scroll.x, scroll.y)
  }
}

export async function copyText(text: string): Promise<void> {
  // Avoid a late fallback overwriting a newer copy while the modern API is pending.
  if (copying) throw new Error('正在复制，请稍后重试')
  copying = true
  try {
    if (window.isSecureContext && navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(text)
        return
      } catch {
        // Permission rejection can still allow the selection-based copy command.
      }
    }
    try {
      // Keep selection and execCommand synchronous to retain the click's activation.
      copyWithSelection(text)
    } catch {
      throw new Error('复制失败，请手动选取文本')
    }
  } finally {
    copying = false
  }
}
