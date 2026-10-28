// State installed by clipboard.spec.ts to observe copies in the page.
type ClipboardTestState = {
  modern: string[]
  reject: (() => void) | undefined
}

declare global {
  interface Window {
    clipboardTest: ClipboardTestState
  }
}

export {}
