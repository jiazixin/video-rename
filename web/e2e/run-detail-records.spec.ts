import { test, expect } from '@playwright/test'
import { mockRunPages } from './run-pages'

test('详情日志和失败记录覆盖整批、独立分页和自动刷新', async ({ page }) => {
  const stamp = new Date().toISOString()
  const run = {
    id: 960,
    task: 'rename',
    source: 'manual',
    status: 'running',
    targets: [1],
    created: stamp,
    started: stamp,
    discovered: 205,
    done: 100,
    skipped: 0,
    failed: 105
  }
  const records = Array.from({ length: 205 }, (_, i) => ({
    id: i + 1,
    entry: i + 1,
    status: i < 100 ? 'done' : 'failed',
    original_path: `clip-${i + 1}.mp4`,
    current_path: `clip-${i + 1}.mp4`,
    message: i < 100 ? '' : `失败原因 ${i + 1}`,
    updated_ms: Date.now() + i,
    attempts: [
      {
        number: 1,
        model_config_title: '测试模型',
        model: 'vision',
        fallback: false,
        started: stamp,
        duration_ms: 500,
        status: i < 100 ? 'success' : 'failed',
        message: ''
      }
    ]
  }))
  let failFailures = false
  const requests: string[] = []
  await mockRunPages(page, [run])
  await page.route('**/api/runs/960', (route) => route.fulfill({ json: run }))
  await page.route('**/api/runs/960/items?*', (route) => {
    const query = new URL(route.request().url()).searchParams
    const view = query.get('view') || 'files'
    requests.push(`${view}:${query.get('page')}`)
    if (view === 'failures' && failFailures)
      return route.fulfill({ status: 503, json: { message: '暂时不可用' } })
    const items =
      view === 'failures'
        ? records.filter((item) => item.status === 'failed').toReversed()
        : view === 'logs'
          ? records.toReversed()
          : records
    const index = Math.min(Math.max(1, Number(query.get('page'))), Math.ceil(items.length / 100))
    return route.fulfill({
      json: { page: index, total: items.length, items: items.slice((index - 1) * 100, index * 100) }
    })
  })
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '批次记录', exact: true }).click()
  await page.getByRole('button', { name: '详情', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '运行 #960' })
  const logs = dialog.locator('#run-detail-logs')
  const failures = dialog.locator('#run-detail-failures')
  await expect(dialog.locator('.run-item')).toHaveCount(100)
  expect(requests.filter((query) => !query.startsWith('files'))).toHaveLength(0)
  await dialog.getByRole('button', { name: /^运行日志/ }).click()
  await dialog.getByRole('button', { name: /^失败记录/ }).click()
  await expect(logs.locator('.detail-log-row')).toHaveCount(100)
  await expect(failures.locator('.detail-failure')).toHaveCount(100)
  await expect(logs.locator('.detail-log-title').first()).toContainText('clip-205.mp4')
  await expect(failures.locator('.detail-failure').first()).toContainText('失败原因 205')
  await expect(logs.getByLabel('运行日志分页', { exact: true })).toContainText('共 205 条')
  await expect(failures.getByLabel('失败记录分页', { exact: true })).toContainText('共 105 条')
  await logs.locator('.attempt-history summary').first().click()
  records.push({
    ...records[204],
    id: 206,
    entry: 206,
    original_path: 'clip-206.mp4',
    current_path: 'clip-206.mp4',
    message: '新增失败',
    updated_ms: Date.now() + 1000
  })
  run.failed++
  run.discovered++
  await expect(logs.locator('.detail-log-title').first()).toContainText('clip-206.mp4', {
    timeout: 10000
  })
  await expect(failures.locator('.detail-failure').first()).toContainText('新增失败', {
    timeout: 10000
  })
  await expect(
    logs.locator('.detail-log-row').filter({ hasText: 'clip-205.mp4' }).locator('.attempt-history')
  ).toHaveAttribute('open', '')
  await logs.getByRole('button', { name: '下页日志' }).click()
  await expect(
    logs.getByLabel('运行日志翻页', { exact: true }).locator('.page-current')
  ).toHaveText('2')
  await failures.getByRole('button', { name: '下页失败' }).click()
  await expect(failures.locator('.detail-failure')).toHaveCount(6)
  await expect(dialog.locator('.run-item').first()).toContainText('clip-1.mp4')
  const before = requests.filter((query) => query === 'logs:2').length
  await expect
    .poll(() => requests.filter((query) => query === 'logs:2').length, { timeout: 10000 })
    .toBeGreaterThan(before)
  await expect(
    logs.getByLabel('运行日志翻页', { exact: true }).locator('.page-current')
  ).toHaveText('2')
  await expect(
    failures.getByLabel('失败记录翻页', { exact: true }).locator('.page-current')
  ).toHaveText('2')
  failFailures = true
  await expect(failures.getByRole('alert')).toContainText('暂时不可用', { timeout: 10000 })
  await expect(failures.locator('.detail-failure')).toHaveCount(6)
  failFailures = false
  await failures.getByRole('button', { name: '重试', exact: true }).click()
  await expect(failures.getByRole('alert')).toHaveCount(0)
  await logs.getByRole('button', { name: '查看最新日志' }).click()
  await failures.getByRole('button', { name: '查看最新失败' }).click()
  await expect(logs.locator('.detail-log-title').first()).toContainText('clip-206.mp4')
  await expect(failures.locator('.detail-failure').first()).toContainText('新增失败')
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBeTruthy()
  expect(await logs.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBeTruthy()
  expect(await failures.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBeTruthy()
  await failures.getByLabel('失败记录分页', { exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: 'test-results/run-detail-records-mobile.png' })
  await dialog.getByRole('button', { name: /^运行日志/ }).click()
  await dialog.getByRole('button', { name: /^失败记录/ }).click()
  const count = requests.filter((query) => !query.startsWith('files')).length
  const filesCount = requests.filter((query) => query.startsWith('files')).length
  await expect
    .poll(() => requests.filter((query) => query.startsWith('files')).length, { timeout: 10000 })
    .toBeGreaterThan(filesCount)
  expect(requests.filter((query) => !query.startsWith('files')).length).toBe(count)
})

test('批次状态刷新不打断日志读取，收起后忽略迟到响应', async ({ page }) => {
  const run = {
    id: 961,
    task: 'extract',
    source: 'manual',
    status: 'running',
    targets: [1],
    created: new Date().toISOString(),
    discovered: 1,
    done: 0,
    skipped: 0,
    failed: 1
  }
  let releaseFirst!: () => void
  let releaseSecond!: () => void
  const first = new Promise<void>((resolve) => {
    releaseFirst = resolve
  })
  const second = new Promise<void>((resolve) => {
    releaseSecond = resolve
  })
  let logRequests = 0
  await mockRunPages(page, [run])
  await page.route('**/api/runs/961', (route) => route.fulfill({ json: run }))
  await page.route('**/api/runs/961/items?*', async (route) => {
    const view = new URL(route.request().url()).searchParams.get('view')
    if (!view) return route.fulfill({ json: { items: [], page: 1, total: 0 } })
    const request = ++logRequests
    if (request === 1) await first
    if (request === 2) await second
    try {
      await route.fulfill({
        json: {
          page: 1,
          total: 1,
          items: [
            {
              id: 1,
              entry: 1,
              status: 'failed',
              original_path: 'clip.mp4',
              current_path: 'clip.mp4',
              message: request === 2 ? '迟到的旧记录' : request === 1 ? '首次记录' : '最新记录',
              updated_ms: Date.now(),
              attempts: []
            }
          ]
        }
      })
    } catch {
      /* A collapsed section cancels its pending request. */
    }
  })
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '批次记录', exact: true }).click()
  await page.getByRole('button', { name: '详情', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '运行 #961' })
  const logs = dialog.locator('#run-detail-logs')
  await dialog.getByRole('button', { name: /^运行日志/ }).click()
  await expect.poll(() => logRequests).toBe(1)
  run.status = 'paused'
  await expect(dialog.locator('.detail-header-status')).toHaveText('已暂停', { timeout: 10000 })
  expect(logRequests).toBe(1)
  releaseFirst()
  await expect(logs).toContainText('首次记录')
  await expect.poll(() => logRequests, { timeout: 10000 }).toBe(2)
  await dialog.getByRole('button', { name: /^运行日志/ }).click()
  await dialog.getByRole('button', { name: /^运行日志/ }).click()
  await expect(logs).toContainText('最新记录')
  releaseSecond()
  await expect(logs).not.toContainText('迟到的旧记录')
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(dialog).toHaveCount(0)
})
