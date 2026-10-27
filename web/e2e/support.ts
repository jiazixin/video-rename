import type { Locator, Page } from '@playwright/test'

/** Returns value, failing the test with a clear message when it is missing. */
export function defined<T>(value: T | null | undefined, what = 'value'): T {
  if (value === null || value === undefined) throw new Error(`expected ${what} to be present`)
  return value
}

/** The library directory ID in a page URL such as http://host/#/library/5. */
export function libraryDirectory(url: string): number {
  return Number(defined(/#\/library\/(\d+)/.exec(url)?.[1], 'library directory in URL'))
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

/** The "current/total页" readout of a pagination control. */
export function pageReadout(navigation: Locator): Locator {
  return navigation.getByText(/^\d+\/\d+页$/)
}
