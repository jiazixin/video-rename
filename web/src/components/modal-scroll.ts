let locks = 0
let oldOverflow = ''
let oldPadding = ''

/** Locks document scrolling until every open dialog has released its lock. */
export function lockScroll(): void {
  if (locks++ !== 0) return
  oldOverflow = document.body.style.overflow
  oldPadding = document.body.style.paddingRight
  const gap = window.innerWidth - document.documentElement.clientWidth
  document.body.style.overflow = 'hidden'
  if (gap) document.body.style.paddingRight = `${gap}px`
}

/** Restores the document's previous scrolling styles after the last dialog. */
export function unlockScroll(): void {
  if (locks === 0 || --locks !== 0) return
  document.body.style.overflow = oldOverflow
  document.body.style.paddingRight = oldPadding
}
