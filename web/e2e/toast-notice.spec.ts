import { test, expect } from '@playwright/test'

test('浮动提示保留重复消息、最新置顶，并独立关闭和到期', async ({ page }) => {
  // Synthetic history and restore responses keep this UI regression isolated.
  await page.route('**/api/logs?*', route => route.fulfill({ json: {
    items: [{ id: 1, category: 'success', action: 'manual', operation: 1,
      old_path: '原名.mp4', new_path: '新名.mp4', created: '2026-10-02T00:00:00Z' }],
    total: 1, page: 1,
  } }))
  await page.route('**/api/logs/batches', route => route.fulfill({ json: [] }))
  await page.route('**/api/operations/1/restore', route => route.fulfill({ json: {} }))
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: async () => {} } })
  })
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '运行记录', exact: true }).click()
  await page.getByRole('button', { name: '详情', exact: true }).click()
  const time = new Date('2026-10-02T01:00:00Z')
  await page.clock.install({ time })
  await page.clock.pauseAt(time)
  const notices = page.locator('.toast-notice')
  const messages = page.locator('.toast-message')
  await page.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(messages).toHaveText(['已复制运行记录详情'])
  await page.clock.runFor(1000)
  await page.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(messages).toHaveText(['已复制运行记录详情', '已复制运行记录详情'])
  await page.clock.runFor(1000)
  await page.getByRole('button', { name: '还原此次命名', exact: true }).click()
  await expect(messages).toHaveText(['名称已还原', '已复制运行记录详情', '已复制运行记录详情'])
  await page.clock.runFor(250)
  // CSS transitions use browser animation time, independently of the timer clock.
  await expect.poll(() => notices.evaluateAll(elements => {
    const bounds = elements.map(element => element.getBoundingClientRect())
    return bounds.length === 3 && bounds.slice(1).every((rect, index) => bounds[index].bottom < rect.top)
  })).toBe(true)
  await page.screenshot({ path: 'test-results/toast-stack.png' })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.clock.runFor(250)
  expect(await notices.first().evaluate(element => element.getBoundingClientRect().right)).toBeLessThanOrEqual(390)
  await page.screenshot({ path: 'test-results/toast-stack-mobile.png' })
  await notices.nth(1).getByRole('button', { name: '关闭提示' }).click()
  await page.clock.runFor(250)
  await expect(messages).toHaveText(['名称已还原', '已复制运行记录详情'])
  // At 5.75 seconds the first copy expires; the restore has its own deadline.
  await page.clock.runFor(3000)
  await expect(messages).toHaveText(['名称已还原'])
  await page.clock.runFor(2000)
  await expect(notices).toHaveCount(0)

  await page.getByRole('button', { name: '详情', exact: true }).click()
  await page.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(notices).toHaveCount(1)
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('button', { name: '文件目录', exact: true }).click()
  await expect(notices).toHaveCount(0)
  await page.clock.runFor(6000)
  await expect(notices).toHaveCount(0)
})
