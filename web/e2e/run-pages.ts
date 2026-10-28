import type { Page } from '@playwright/test'

export async function mockRunPages(page: Page, runs: Record<string, unknown>[]) {
  await page.route('**/api/runs?*', (route) => {
    const query = new URL(route.request().url()).searchParams
    const filtered = runs.filter(
      (run) =>
        (query.get('category') === 'all' ||
          (run.source === 'schedule' || Number(run.plan) > 0) ===
            (query.get('category') === 'automatic')) &&
        (!query.get('task') || (run.task || 'rename') === query.get('task'))
    )
    const size = Number(query.get('limit')) || 50,
      index = Math.floor(Number(query.get('offset') || 0) / size) + 1 || 1
    return route.fulfill({
      json: {
        items: filtered.slice((index - 1) * size, index * size),
        total: filtered.length,
        offset: (index - 1) * size,
        limit: size
      }
    })
  })
}
