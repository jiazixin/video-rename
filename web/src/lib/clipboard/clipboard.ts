/** Why a copy failed; the interface maps the code to its text. */
export type ClipboardErrorCode = 'copy_busy' | 'copy_manual'

/** A failed copyText call. message is English developer text. */
export class ClipboardError extends Error {
  readonly code: ClipboardErrorCode

  constructor(code: ClipboardErrorCode) {
    super(code.replaceAll('_', ' '))
    this.name = 'ClipboardError'
    this.code = code
  }
}

let copying = false

/** Copies text through the secure-context Clipboard API or reports a manual-copy requirement. */
export async function copyText(text: string): Promise<void> {
  if (copying) throw new ClipboardError('copy_busy')
  if (!window.isSecureContext || !('clipboard' in navigator))
    throw new ClipboardError('copy_manual')
  copying = true
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    throw new ClipboardError('copy_manual')
  } finally {
    copying = false
  }
}
