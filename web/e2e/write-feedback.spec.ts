import { toasts } from './support'
import { test, expect } from '@playwright/test'

// A failed write is reported once: next to the control when a dialog or form
// has an error region, otherwise as a toast.
test('写入失败只提示一次：弹窗内就地显示，卡片操作使用浮动通知', async ({ page }) => {
  const plan = {
    id: 71,
    name: '每日旅行整理',
    task: 'extract',
    model_config_id: 0,
    targets: [1],
    target_names: ['2026/旅行记录'],
    recursive: true,
    kind: 'daily',
    expression: '02:00',
    timezone: 'Asia/Shanghai',
    enabled: true,
    next: '2026-11-12T02:00:00+08:00',
    last: '',
    etag: '"plan-71-1"',
    version: 1
  }
  const failure = (status: number, code: string) => ({
    status,
    json: { error: { code, message: 'test failure' } }
  })
  await page.route('**/api/schedules', (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ json: { items: [plan] } })
      : route.fulfill(failure(500, 'internal'))
  )
  await page.route('**/api/schedules/71', (route) => route.fulfill(failure(409, 'conflict')))
  await page.route('**/api/schedules/71/run', (route) => route.fulfill(failure(500, 'internal')))

  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '计划管理', exact: true }).click()
  const card = page.getByRole('article', { name: '每日旅行整理', exact: true })
  await expect(card).toBeVisible()

  // A card has no error region, so its actions report through a toast only.
  await card.getByRole('switch', { name: '暂停 每日旅行整理' }).click()
  await expect(toasts(page)).toHaveCount(1)
  await expect(page.getByRole('alert')).toHaveCount(0)
  await toasts(page).getByRole('button', { name: '关闭提示' }).click()
  await expect(toasts(page)).toHaveCount(0)
  await card.getByRole('button', { name: '立即运行', exact: true }).click()
  await expect(toasts(page)).toHaveCount(1)
  await expect(page.getByRole('alert')).toHaveCount(0)
  await toasts(page).getByRole('button', { name: '关闭提示' }).click()
  await expect(toasts(page)).toHaveCount(0)

  // The delete confirmation shows the failure itself and stays open for a retry.
  await card.getByRole('button', { name: '删除 每日旅行整理' }).click()
  const confirm = page.getByRole('dialog')
  await confirm.getByRole('button', { name: '删除计划', exact: true }).click()
  await expect(confirm.getByRole('alert')).toHaveCount(1)
  await expect(page.getByRole('alert')).toHaveCount(1)
  await expect(toasts(page)).toHaveCount(0)
  await expect(confirm.getByRole('button', { name: '删除计划', exact: true })).toBeEnabled()
  await confirm.getByRole('button', { name: '取消', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  // So does the plan form.
  await page.getByRole('button', { name: '新建自动计划', exact: true }).click()
  const form = page.getByRole('dialog')
  // A frame extraction plan needs no model configuration.
  await form.getByRole('combobox', { name: '计划任务类型' }).click()
  await page.getByRole('option', { name: '抽帧', exact: true }).click()
  await form.getByLabel('计划名称', { exact: true }).fill('每周整理')
  await form.getByLabel('选择 2026 · 旅行记录', { exact: true }).check()
  await form.getByRole('button', { name: '保存计划', exact: true }).click()
  await expect(form.getByRole('alert')).toHaveCount(1)
  await expect(page.getByRole('alert')).toHaveCount(1)
  await expect(toasts(page)).toHaveCount(0)
  await expect(form.getByLabel('计划名称', { exact: true })).toHaveValue('每周整理')
})

test('批量还原部分失败只在弹窗内汇总，不再弹浮动通知', async ({ page }) => {
  const entry = (id: number) => ({
    id,
    parent: 1,
    name: `还原-${id}.mp4`,
    kind: 'file',
    size_bytes: 1,
    mtime: '2026-10-01T00:00:00.000Z',
    etag: '"v1"',
    version: 1,
    processed: '',
    restore_id: id + 5000,
    task_status: ''
  })
  const entries = [entry(2001), entry(2002)]
  await page.route('**/api/directories/*/entries?*', (route) =>
    route.fulfill({
      json: {
        offset: 0,
        limit: 100,
        total: entries.length,
        items: entries,
        generation: '1',
        refreshing: false,
        refreshed: '',
        directory: { ...entry(1), name: '媒体库', kind: 'directory', restore_id: 0 },
        breadcrumbs: []
      }
    })
  )
  await page.route('**/api/entries/status?*', (route) =>
    route.fulfill({ json: { items: entries } })
  )
  await page.route('**/api/operations/*/restore', (route) =>
    route.request().url().includes('/7002/')
      ? route.fulfill({
          status: 409,
          json: { error: { code: 'conflict', message: 'test failure' } }
        })
      : route.fulfill({ json: {} })
  )
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '多选', exact: true }).click()
  for (const e of entries) await page.getByLabel(`选择 ${e.name}`, { exact: true }).check()
  await page
    .getByRole('group', { name: '批量操作' })
    .getByRole('button', { name: '还原', exact: true })
    .click()
  const dialog = page.getByRole('dialog', { name: '批量还原', exact: true })
  await dialog.getByRole('button', { name: '确认批量还原', exact: true }).click()
  await expect(dialog.getByRole('alert')).toContainText('已还原 1 项，失败 1 项')
  await expect(dialog.getByRole('alert')).toContainText('还原-2002.mp4：')
  await expect(toasts(page)).toHaveCount(0)
})
