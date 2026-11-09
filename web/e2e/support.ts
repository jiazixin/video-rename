import type { Locator, Page } from '@playwright/test'

/** Returns value, failing the test with a clear message when it is missing. */
export function defined<T>(value: T | null | undefined, what = 'value'): T {
  if (value === null || value === undefined) throw new Error(`expected ${what} to be present`)
  return value
}

/** The library directory ID in a page URL such as http://host/library/5. */
export function libraryDirectory(url: string): number {
  return Number(
    defined(/^\/library\/(\d+)/.exec(new URL(url).pathname)?.[1], 'library directory in URL')
  )
}

function pageOf(scope: Page | Locator): Page {
  return 'page' in scope ? scope.page() : scope
}

/** Toast notices in display order; unlike other live regions each has a close button. */
export function toasts(page: Page): Locator {
  return page.getByRole('status').filter({ has: page.getByRole('button', { name: '关闭提示' }) })
}

/** Body rows of the tables in scope; header rows hold column headers, not cells. */
export function bodyRows(scope: Page | Locator): Locator {
  return scope.getByRole('row').filter({ has: pageOf(scope).getByRole('cell') })
}

/** Waits until every finite CSS animation has finished, so geometry reads final positions. */
export async function settleAnimations(page: Page): Promise<void> {
  // Vue holds the enter/leave start state for a frame even when reduced motion leaves no
  // animation to wait on, e.g. a dialog's first frame is scaled and shifted.
  await page.waitForFunction(
    () => !document.querySelector('[class*="-enter-"], [class*="-leave-"]')
  )
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((animation) => Number.isFinite(animation.effect?.getComputedTiming().endTime))
        // A cancelled animation rejects `finished`; it has stopped moving all the same.
        .map((animation) => animation.finished.catch(() => undefined))
    )
  )
}

/** The "current/total页" readout of a pagination control. */
export function pageReadout(navigation: Locator): Locator {
  return navigation.getByText(/^\d+\/\d+页$/)
}
