import { test, expect } from '@playwright/test'
import { mockRunPages } from './run-pages'

test('运行记录分类分页、条数、刷新和窄屏', async ({ page }) => {
  const runs = Array.from({ length: 123 }, (_, i) => ({
    id: 1000 - i,
    source: 'manual',
    plan: 0,
    status: 'completed',
    discovered: 1,
    done: 1,
    skipped: 0,
    failed: 0,
    created: '2026-09-26T00:00:00Z'
  }))
  await mockRunPages(page, [
    ...runs,
    ...Array.from({ length: 51 }, (_, i) => ({
      ...runs[i],
      id: 2000 - i,
      source: 'schedule',
      plan: 7
    }))
  ])
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '批次记录', exact: true }).click()
  const pager = page.getByLabel('批次记录分页')
  await expect(pager).toContainText('共 174 个批次')
  await page.getByRole('combobox', { name: '运行来源', exact: true }).click()
  await page.getByRole('option', { name: '手动运行', exact: true }).click()
  await expect(pager).toContainText('共 123 个批次 · 第 1 / 3 页 · 当前页 50 条')
  await expect(pager.getByRole('button', { name: '上一页' })).toBeDisabled()
  await pager.getByRole('button', { name: '下一页' }).click()
  await expect(page.getByRole('article').first()).toHaveAccessibleName('批次 #950')
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(pager).toContainText('第 2 / 3 页')
  await expect(page.getByRole('article').first()).toHaveAccessibleName('批次 #950')
  await page.getByRole('combobox', { name: '运行来源', exact: true }).click()
  await page.getByRole('option', { name: '计划触发', exact: true }).click()
  await expect(pager).toContainText('共 51 个批次 · 第 1 / 2 页')
  await page.getByRole('combobox', { name: '运行来源', exact: true }).click()
  await page.getByRole('option', { name: '手动运行', exact: true }).click()
  await expect(pager).toContainText('第 2 / 3 页')
  await pager.getByRole('button', { name: '下一页' }).click()
  await expect(pager).toContainText('当前页 23 条')
  await expect(pager.getByRole('button', { name: '下一页' })).toBeDisabled()
  await pager.getByRole('combobox', { name: '每页批次数' }).click()
  await page.getByRole('option', { name: '100 条 / 页', exact: true }).click()
  await expect(pager).toContainText('第 1 / 2 页 · 当前页 100 条')
  await pager.getByRole('combobox', { name: '每页批次数' }).click()
  await page.getByRole('option', { name: '200 条 / 页', exact: true }).click()
  await expect(pager).toContainText('第 1 / 1 页 · 当前页 123 条')
  await page.setViewportSize({ width: 390, height: 844 })
  await pager.scrollIntoViewIfNeeded()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
  await page.screenshot({ path: 'test-results/run-pagination-mobile.png' })
  await page.getByRole('combobox', { name: '任务类型', exact: true }).click()
  await page.getByRole('option', { name: '抽帧', exact: true }).click()
  await expect(pager).toContainText('共 0 个批次')
  await expect(page.getByText('没有符合筛选条件的批次', { exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: '任务类型', exact: true }).click()
  await page.getByRole('option', { name: '命名', exact: true }).click()
  await expect(pager).toContainText('共 123 个批次')
})

test('批次顶部组合筛选、请求参数与重置', async ({ page }) => {
  let query = new URLSearchParams()
  await page.route('**/api/runs?*', (route) => {
    query = new URL(route.request().url()).searchParams
    return route.fulfill({
      json: {
        items: [],
        total: 0,
        page: 1,
        size: 50,
        profiles: [
          { id: 41, title: '配置 A' },
          { id: 42, title: '配置 B' }
        ]
      }
    })
  })
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '批次记录', exact: true }).click()
  for (const [label, option] of [
    ['时间范围', '自定义时间'],
    ['模型配置筛选', '配置 B'],
    ['批次状态', '部分失败'],
    ['批次耗时', '5–30 分钟']
  ] as const) {
    await page.getByRole('combobox', { name: label, exact: true }).click()
    await page.getByRole('option', { name: option, exact: true }).click()
  }
  await page.getByLabel('开始时间', { exact: true }).fill('2026-09-01T00:00')
  await page.getByLabel('结束时间', { exact: true }).fill('2026-09-26T23:59')
  await expect
    .poll(() => [
      query.get('profile'),
      query.get('status'),
      query.get('duration'),
      !!query.get('from'),
      !!query.get('to')
    ])
    .toEqual(['42', 'partial_failure', '5to30', true, true])
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
  await page.screenshot({ path: 'test-results/batch-filters-mobile.png', fullPage: true })
  await page.getByRole('button', { name: '重置筛选' }).click()
  await expect
    .poll(() => [
      query.get('profile'),
      query.get('status'),
      query.get('duration'),
      query.has('from'),
      query.has('to')
    ])
    .toEqual(['', '', '', false, false])
})
