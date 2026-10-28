import { test, expect } from '@playwright/test'
import { mockRunPages } from './run-pages'
import { toasts } from './support'
test('本页选择跨页保留、批次操作和折叠日志复制', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.route('**/api/directories/1/entries?kind=directory*', async (route) => {
    const second =
      String(
        Math.floor(Number(new URL(route.request().url()).searchParams.get('offset') || 0) / 50) + 1
      ) === '2'
    const directory = { id: 1, name: '媒体库', kind: 'directory' }
    await route.fulfill({
      json: {
        directory,
        breadcrumbs: [directory],
        items: (second ? [104] : [102, 103]).map((id) => ({
          id,
          name: `目录${id}`,
          kind: 'directory'
        })),
        offset: second ? 50 : 0,
        limit: 50,
        total: 51,
        refreshing: false
      }
    })
  })
  await page.getByRole('button', { name: '计划管理', exact: true }).click()
  await page.getByRole('button', { name: '新建自动计划', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: '全选本页', exact: true }).click()
  await expect(dialog).toContainText('本页已选 2 / 2 · 共选 2 个目录')
  await dialog.getByRole('button', { name: '下页目录' }).click()
  await expect(dialog).toContainText('本页已选 0 / 1 · 共选 2 个目录')
  await dialog.getByRole('button', { name: '反选本页' }).click()
  await expect(dialog).toContainText('共选 3 个目录')
  await dialog.getByRole('button', { name: '取消本页选择' }).click()
  await expect(dialog).toContainText('共选 2 个目录')
  await dialog.getByRole('button', { name: '上页目录' }).click()
  await expect(dialog).toContainText('本页已选 2 / 2')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: 'test-results/picker-batch-mobile.png' })
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBeTruthy()
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await page.setViewportSize({ width: 1440, height: 1000 })
  await mockRunPages(page, [
    {
      id: 900,
      status: 'completed',
      source: 'manual',
      created: '2026-09-23T02:04:11+08:00',
      started: '2026-09-23T02:04:12+08:00',
      finished: '2026-09-23T02:23:07+08:00',
      done: 2,
      discovered: 2,
      failed: 0,
      skipped: 0,
      token_count: 982800,
      token_records: 4,
      token_estimated_records: 0,
      targets: [1]
    },
    {
      id: 901,
      status: 'running',
      source: 'manual',
      created: '2026-09-24T02:12:03+08:00',
      started: '2026-09-24T02:12:04+08:00',
      done: 1,
      discovered: 2,
      failed: 0,
      skipped: 0,
      token_count: 0,
      token_records: 0,
      token_estimated_records: 0,
      targets: [1]
    }
  ])
  await page.route('**/api/runs/900/restore', (route) =>
    route.fulfill({ json: { restored: 2, skipped: 0, failed: 0, errors: [] } })
  )
  await page.getByRole('button', { name: '批次记录', exact: true }).click()
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  const row = page.getByRole('article', { name: '批次 #900' })
  await expect(row.getByRole('button', { name: '取消', exact: true })).toBeDisabled()
  await expect(
    page
      .getByRole('article')
      .filter({ hasText: '#901' })
      .getByRole('button', { name: '还原批次', exact: true })
  ).toBeDisabled()
  await page.screenshot({ path: 'test-results/run-batch-actions.png' })
  await row.getByRole('button', { name: '还原批次', exact: true }).click()
  await dialog.getByRole('button', { name: '确认还原批次' }).click()
  await expect(toasts(page).first()).toContainText('已还原 2 项')
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  await page.route('**/api/logs?*', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: 999,
            category: 'failure',
            action: 'automatic',
            old_path: 'bad.mp4',
            message: 'extract_insufficient_frames: 计划 20 帧全部失败',
            run: 900
          }
        ],
        total: 1,
        offset: 0,
        limit: 100
      }
    })
  )
  await page.route('**/api/logs/999/diagnostics', (route) =>
    route.fulfill({
      json: {
        request: '',
        response: '',
        processing: '采样时间 1s · 首次抽帧\nffmpeg 退出码 7: decoder unavailable'
      }
    })
  )
  await page.getByRole('button', { name: '运行记录', exact: true }).click()
  await page.getByRole('button', { name: '失败记录', exact: true }).click()
  await page.getByRole('button', { name: '详情', exact: true }).click()
  await expect(dialog.getByText(/decoder unavailable/)).toHaveCount(0)
  await dialog.getByRole('button', { name: '抽帧 / 媒体诊断日志' }).click()
  await expect(dialog.getByText(/decoder unavailable/)).toBeVisible()
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await dialog.getByRole('button', { name: '复制', exact: true }).click()
  await expect(dialog.getByRole('button', { name: '已复制', exact: true })).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('decoder unavailable')
  await page.screenshot({ path: 'test-results/extract-diagnostics.png' })
})
