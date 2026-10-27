import { defined, toasts } from './support'
import { test, expect } from '@playwright/test'

test('保存系统设置不移动表单，切换分类清理通知，通知渠道保存和测试使用浮动反馈', async ({
  page
}) => {
  let settings = {
    running_batch_limit: 32,
    waiting_batch_limit: 1000,
    extract_workers: 8,
    cache_gb: 10,
    stability_seconds: 30,
    retry_enabled: false,
    retry_count: 0,
    fallback_model_id: 0,
    success_cache_mode: 'immediate',
    success_cache_hours: 72,
    idle_cache_hours: 72
  }
  let fail = false
  await page.route('**/api/system/settings', (route) => {
    if (route.request().method() === 'PUT') {
      if (fail)
        return route.fulfill({
          status: 500,
          json: {
            error: {
              code: 'internal',
              message: 'test failure',
              details: { detail: '保存失败，请重试' }
            }
          }
        })
      settings = route.request().postDataJSON()
      return route.fulfill({ json: settings })
    }
    return route.fulfill({
      json: {
        settings,
        cache: {
          path: '/test/cache',
          used_bytes: 0,
          reserved_bytes: 0,
          entries: 0
        }
      }
    })
  })
  const channel = {
    kind: 'webhook',
    enabled: false,
    failure_only: false,
    has_endpoint: true,
    has_token: false,
    has_secret: false,
    chat_id: ''
  }
  await page.route('**/api/notifications', (route) => route.fulfill({ json: { items: [channel] } }))
  await page.route('**/api/notifications/history?*', (route) =>
    route.fulfill({
      json: {
        items: [],
        total: 0,
        page: 1,
        size: 50
      }
    })
  )
  await page.route('**/api/notifications/webhook', (route) => route.fulfill({ json: {} }))
  await page.route('**/api/notifications/webhook/test', (route) => route.fulfill({ json: {} }))
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '系统设置', exact: true }).click()
  await page.getByLabel('同时抽帧的视频数', { exact: true }).fill('3')
  const save = page.getByRole('button', { name: '保存系统设置', exact: true })
  await save.scrollIntoViewIfNeeded()
  const before = await save.boundingBox()
  await save.click()
  const notice = toasts(page)
  await expect(notice).toContainText('设置已保存')
  await expect(page.getByRole('main').getByText('设置已保存')).toHaveCount(0)
  const after = await save.boundingBox()
  expect(Math.abs(defined(before).y - defined(after).y)).toBeLessThan(1)
  await notice.getByRole('button', { name: '关闭提示' }).click()
  await expect(notice).toHaveCount(0)
  fail = true
  await page.getByLabel('同时抽帧的视频数', { exact: true }).fill('4')
  await save.click()
  await expect(page.getByRole('alert')).toContainText('保存失败')
  await expect(notice).toHaveCount(0)
  fail = false
  await save.click()
  await expect(notice).toHaveCount(1)
  await page.getByRole('button', { name: '通知设置', exact: true }).click()
  await expect(notice).toHaveCount(0)
  const webhook = page.getByRole('form', { name: '自定义', exact: true })
  await webhook.getByRole('button', { name: '保存设置', exact: true }).click()
  await expect(toasts(page).first()).toHaveText('自定义设置已保存')
  await webhook.getByRole('button', { name: '发送测试', exact: true }).click()
  await expect(toasts(page).first()).toContainText('自定义测试通知已发送')
  await expect(webhook.getByText('自定义测试通知已发送')).toHaveCount(0)
  await page.setViewportSize({ width: 390, height: 844 })
  expect(
    await notice.first().evaluate((el) => el.getBoundingClientRect().right)
  ).toBeLessThanOrEqual(390)
  await page.screenshot({ path: 'test-results/settings-toast-mobile.png' })
  await page.getByRole('button', { name: '文件目录', exact: true }).click()
  await expect(notice).toHaveCount(0)
})

test('浮动提示保留重复消息、最新置顶，并独立关闭和到期', async ({ page }) => {
  // Synthetic history and restore responses keep this UI regression isolated.
  await page.route('**/api/logs?*', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: 1,
            category: 'success',
            action: 'manual',
            operation: 1,
            old_path: '原名.mp4',
            new_path: '新名.mp4',
            created: '2026-10-02T00:00:00Z'
          }
        ],
        total: 1,
        page: 1
      }
    })
  )
  await page.route('**/api/logs/batches', (route) => route.fulfill({ json: { items: [] } }))
  await page.route('**/api/operations/1/restore', (route) => route.fulfill({ json: {} }))
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
  const notices = toasts(page)
  await page.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(notices).toHaveText(['已复制运行记录详情'])
  await page.clock.runFor(1000)
  await page.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(notices).toHaveText(['已复制运行记录详情', '已复制运行记录详情'])
  await page.clock.runFor(1000)
  await page.getByRole('button', { name: '还原此次命名', exact: true }).click()
  await expect(notices).toHaveText(['名称已还原', '已复制运行记录详情', '已复制运行记录详情'])
  await page.clock.runFor(250)
  // CSS transitions use browser animation time, independently of the timer clock.
  await expect
    .poll(() =>
      notices.evaluateAll((elements) => {
        const bounds = elements.map((element) => element.getBoundingClientRect())
        return (
          bounds.length === 3 &&
          bounds.slice(1).every((rect, index) => (bounds[index]?.bottom ?? Infinity) < rect.top)
        )
      })
    )
    .toBe(true)
  await page.screenshot({ path: 'test-results/toast-stack.png' })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.clock.runFor(250)
  expect(
    await notices.first().evaluate((element) => element.getBoundingClientRect().right)
  ).toBeLessThanOrEqual(390)
  await page.screenshot({ path: 'test-results/toast-stack-mobile.png' })
  await notices.nth(1).getByRole('button', { name: '关闭提示' }).click()
  await page.clock.runFor(250)
  await expect(notices).toHaveText(['名称已还原', '已复制运行记录详情'])
  // At 5.75 seconds the first copy expires; the restore has its own deadline.
  await page.clock.runFor(3000)
  await expect(notices).toHaveText(['名称已还原'])
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
