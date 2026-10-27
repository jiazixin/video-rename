import { test, expect, type Page } from '@playwright/test'

async function openNotifications(page: Page) {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '系统设置', exact: true }).click()
  await page.getByRole('button', { name: '通知设置', exact: true }).click()
}

test('通知渠道顺序、发送记录分页、刷新、条数和窄屏', async ({ page }) => {
  let rows = Array.from({ length: 123 }, (_, i) => ({
    id: 123 - i,
    run: 123 - i,
    kind: 'dingtalk',
    status: 'sent',
    attempts: 1,
    error: '',
    updated: '2026-10-01T01:00:00Z'
  }))
  let requests = 0
  await page.route('**/api/notifications/history?*', (route) => {
    requests++
    const query = new URL(route.request().url()).searchParams
    const size = Number(query.get('size')),
      current = Math.min(Number(query.get('page')), Math.max(1, Math.ceil(rows.length / size)))
    return route.fulfill({
      json: {
        items: rows.slice((current - 1) * size, current * size),
        total: rows.length,
        page: current,
        size
      }
    })
  })
  await openNotifications(page)
  await expect(page.locator('.notification-card h2')).toHaveText([
    '钉钉',
    '企业微信',
    'Bark',
    'Telegram',
    '自定义'
  ])
  const pager = page.getByLabel('通知发送记录分页', { exact: true }),
    table = page.locator('.notification-settings tbody')
  await expect(pager).toContainText('共 123 条记录 · 第 1 / 3 页 · 当前页 50 条')
  await expect(table.locator('tr')).toHaveCount(50)
  await expect(pager.getByRole('button', { name: '上一页发送记录' })).toBeDisabled()
  await pager.getByRole('button', { name: '下一页发送记录' }).click()
  await expect(pager).toContainText('第 2 / 3 页')
  await expect(table.locator('tr').first().locator('td').first()).toHaveText('#73')
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(pager).toContainText('第 2 / 3 页')
  const before = requests
  await expect.poll(() => requests, { timeout: 10000 }).toBeGreaterThan(before)
  await expect(table.locator('tr').first().locator('td').first()).toHaveText('#73')
  await pager.getByLabel('跳转页码', { exact: true }).fill('3')
  await pager.getByRole('button', { name: '跳转', exact: true }).click()
  await expect(pager).toContainText('第 3 / 3 页 · 当前页 23 条')
  await expect(pager.getByRole('button', { name: '下一页发送记录' })).toBeDisabled()
  await pager.getByRole('combobox', { name: '每页发送记录数' }).click()
  await page.getByRole('option', { name: '100 条 / 页', exact: true }).click()
  await expect(pager).toContainText('第 1 / 2 页 · 当前页 100 条')
  await pager.getByRole('combobox', { name: '每页发送记录数' }).click()
  await page.getByRole('option', { name: '200 条 / 页', exact: true }).click()
  await expect(pager).toContainText('第 1 / 1 页 · 当前页 123 条')
  await pager.scrollIntoViewIfNeeded()
  await page.screenshot({ path: 'test-results/notification-pagination-desktop.png' })
  await page.setViewportSize({ width: 390, height: 844 })
  await pager.scrollIntoViewIfNeeded()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
  await page.screenshot({ path: 'test-results/notification-pagination-mobile.png' })
  rows = []
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(pager).toContainText('共 0 条记录 · 第 1 / 1 页 · 当前页 0 条')
  await expect(
    page.getByText('暂无发送记录。保存并启用渠道后，新批次结束时会在这里显示结果。', {
      exact: true
    })
  ).toBeVisible()
})

test('发送记录迟到响应不会覆盖新的每页条数', async ({ page }) => {
  let release!: () => void
  const delayed = new Promise<void>((resolve) => (release = resolve))
  await page.route('**/api/notifications/history?*', async (route) => {
    const query = new URL(route.request().url()).searchParams,
      size = Number(query.get('size'))
    if (size === 50) await delayed
    const rows = Array.from({ length: size }, (_, i) => ({
      id: size - i,
      run: size - i,
      kind: 'bark',
      status: 'sent',
      attempts: 1,
      error: '',
      updated: '2026-10-01T01:00:00Z'
    }))
    await route.fulfill({ json: { items: rows, total: 123, page: 1, size } })
  })
  await openNotifications(page)
  const pager = page.getByLabel('通知发送记录分页', { exact: true })
  await pager.getByRole('combobox', { name: '每页发送记录数' }).click()
  await page.getByRole('option', { name: '100 条 / 页', exact: true }).click()
  await expect(pager).toContainText('第 1 / 2 页 · 当前页 100 条')
  const response = page.waitForResponse((r) =>
    r.url().includes('/api/notifications/history?page=1&size=50')
  )
  release()
  await response
  await expect(page.locator('.notification-settings tbody tr')).toHaveCount(100)
  await expect(pager.getByRole('combobox', { name: '每页发送记录数' })).toContainText('100 条 / 页')
})
