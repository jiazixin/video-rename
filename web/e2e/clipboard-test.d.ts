// State installed by clipboard.spec.ts to observe copies in the page.
type ClipboardTestState = {
  modern: string[]
  fallback: string[]
  fallbackAllowed: boolean
  reject: (() => void) | undefined
}

declare global {
  interface Window {
    clipboardTest: ClipboardTestState
  }
}

export {}
