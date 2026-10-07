import { test, expect, type Page } from '@playwright/test'
import { mockRunPages } from './run-pages'
import { recordPage } from './run-record-pages'

const sampleRun = () => ({
  id: 960,
  task: 'rename',
  source: 'manual',
  status: 'running',
  targets: [1],
  target_names: ['素材'],
  created: '2026-10-07T00:00:00Z',
  discovered: 205,
  done: 100,
  skipped: 0,
  failed: 104
})
const records = () =>
  Array.from({ length: 205 }, (_, i) => ({
    id: i + 1,
    entry: i + 1,
    status: i === 204 ? 'queued' : i < 100 ? 'done' : 'failed',
    original_path: `${i < 103 ? '上海' : '杭州'} / clip-${String(i + 1).padStart(3, '0')}.mp4`,
    current_path: '当前目录 / 当前文件.mp4',
    new_name: i < 100 ? `新名称-${i}.mp4` : '',
    operation_status: i < 100 ? 'committed' : '',
    message: i < 100 ? '' : `失败原因 ${i + 1}`,
    updated_ms: 1791331200000,
    attempts: Array.from({ length: 4 }, (_, n) => ({
      number: n + 1,
      model_config_title: '测试模型',
      model: n === 3 ? 'fallback' : 'vision',
      fallback: n === 3,
      started: '2026-10-07T00:00:00Z',
      duration_ms: 500,
      status: i < 100 && n === 3 ? 'success' : 'failed',
      message: `第 ${n + 1} 次请求结果`
    }))
  }))
async function open(page: Page, run = sampleRun()) {
  await mockRunPages(page, [run])
  await page.route('**/api/runs/960', (r) => r.fulfill({ json: run }))
  await page.request.post('/api/auth/login', {
    data: { username: 'admin', password: 'test-password-123' }
  })
  await page.goto('/')
  await page.getByRole('button', { name: '批次记录', exact: true }).click()
  await page.getByRole('button', { name: '详情', exact: true }).click()
  return page.getByRole('dialog', { name: '运行 #960' })
}

test('目录统计覆盖整批，筛选分页保留排队文件与全部尝试', async ({ page }) => {
  const items = records(),
    run = sampleRun()
  let fail = false
  const requests: string[] = []
  await page.route('**/api/runs/960/items?*', (route) => {
    const query = new URL(route.request().url()).searchParams
    requests.push(query.toString())
    return fail
      ? route.fulfill({ status: 503, json: { message: '暂时不可用' } })
      : route.fulfill({ json: recordPage(items, query) })
  })
  const dialog = await open(page, run),
    body = dialog.locator('#run-detail-records')
  await expect(body.locator('.detail-record-file')).toHaveCount(100)
  await expect(dialog.getByRole('button', { name: /^运行记录/ })).toContainText(
    '205 个文件 · 2 个目录'
  )
  await expect(body.locator('.detail-directory-toggle').first()).toContainText('103 个文件')
  await expect(body).toContainText('本页 100 / 103')
  const attempts = body.locator('.detail-attempt-toggle').first()
  await attempts.click()
  await expect(body.locator('.detail-attempt:visible')).toHaveCount(4)
  const content = dialog.locator('.modal-content')
  await content.evaluate((el) => (el.scrollTop = 250))
  const before = requests.length
  await expect.poll(() => requests.length, { timeout: 10000 }).toBeGreaterThan(before)
  await expect(attempts).toHaveAttribute('aria-expanded', 'true')
  expect(await content.evaluate((el) => el.scrollTop)).toBeCloseTo(250, 0)
  await body.getByRole('button', { name: '下页文件' }).click()
  await expect(body.locator('.detail-directory')).toHaveCount(2)
  await expect(body.locator('.page-current')).toHaveText('2')
  await body.getByRole('button', { name: '下页文件' }).click()
  await expect(body.locator('.detail-record-file')).toHaveCount(5)
  await expect(body).toContainText('等待处理')
  await body.getByRole('button', { name: /^最终失败/ }).click()
  await expect(body.locator('.page-current')).toHaveText('1')
  await expect(body.getByLabel('运行记录分页', { exact: true })).toContainText('共 104 个文件')
  await body.getByRole('combobox', { name: '记录目录筛选' }).click()
  await page.getByRole('option', { name: '上海（103）', exact: true }).click()
  await expect(body.locator('.detail-record-file')).toHaveCount(3)
  const failed = body.locator('.detail-record-file').first()
  await failed.locator('.detail-attempt-toggle').click()
  await expect(failed.locator('.detail-attempt:visible')).toHaveCount(4)
  await expect(failed).toContainText('重试第 3 次 · 兜底模型')
  await expect(failed).toContainText('未生成有效名称')
  await body.locator('.detail-directory-toggle').click()
  await expect(body.locator('.detail-directory-toggle')).toHaveAttribute('aria-expanded', 'false')
  const collapsed = requests.length
  await expect.poll(() => requests.length, { timeout: 10000 }).toBeGreaterThan(collapsed)
  await expect(body.locator('.detail-directory-toggle')).toHaveAttribute('aria-expanded', 'false')
  await body.locator('.detail-directory-toggle').click()
  fail = true
  await expect(body.getByRole('alert')).toContainText('保留上次读取', { timeout: 10000 })
  await expect(body.locator('.detail-record-file')).toHaveCount(3)
  fail = false
  await body.getByRole('button', { name: '重试', exact: true }).click()
  await expect(body.getByRole('alert')).toHaveCount(0)
  await page.setViewportSize({ width: 320, height: 844 })
  expect(await body.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBeTruthy()
  await failed.scrollIntoViewIfNeeded()
  await page.screenshot({ path: 'test-results/run-directory-failures-320.png' })
  await body.getByRole('combobox', { name: '记录目录筛选' }).focus()
  await page.keyboard.press('Enter')
  await page.keyboard.press('Home')
  await page.keyboard.press('Enter')
  await expect(body.getByRole('combobox', { name: '记录目录筛选' })).toContainText('全部目录')
  await expect(body.getByRole('combobox', { name: '记录目录筛选' })).toBeFocused()
})

test('加载、错误、空记录与无失败状态不混淆', async ({ page }) => {
  const run = sampleRun()
  run.status = 'completed'
  let release!: () => void
  const pending = new Promise<void>((r) => (release = r))
  let fail = true
  await page.route('**/api/runs/960/items?*', async (route) => {
    await pending
    return fail
      ? route.fulfill({ status: 503, json: { message: '读取失败' } })
      : route.fulfill({ json: recordPage([], new URL(route.request().url()).searchParams) })
  })
  const dialog = await open(page, run),
    body = dialog.locator('#run-detail-records')
  await expect(body.getByRole('status')).toHaveText('正在读取运行记录…')
  await expect(dialog.getByRole('button', { name: /^运行记录/ })).not.toContainText('0 个文件')
  release()
  await expect(body.getByRole('alert')).toContainText('读取失败')
  await expect(body.getByRole('status')).toHaveText('暂时无法读取运行记录')
  fail = false
  await body.getByRole('button', { name: '重试', exact: true }).click()
  await expect(body.getByRole('status')).toHaveText('当前范围没有文件任务。')
  await body.getByRole('button', { name: /^最终失败/ }).click()
  await expect(body.getByRole('status')).toHaveText('当前范围没有最终失败的文件。')
})

test('切换筛选忽略迟到响应，终态最后刷新后停止轮询', async ({ page }) => {
  const run = sampleRun(),
    items = records()
  let count = 0
  let release!: () => void
  const pending = new Promise<void>((r) => (release = r))
  await page.route('**/api/runs/960/items?*', async (route) => {
    const n = ++count
    const query = new URL(route.request().url()).searchParams
    if (n === 1) await pending
    try {
      await route.fulfill({ json: recordPage(items, query) })
    } catch {
      /* cancelled request */
    }
  })
  const dialog = await open(page, run),
    body = dialog.locator('#run-detail-records')
  await expect.poll(() => count).toBe(1)
  await body.getByRole('button', { name: /^最终失败/ }).click()
  await expect(body.getByLabel('运行记录分页', { exact: true })).toContainText('共 104 个文件')
  release()
  await expect(body.locator('.status-badge[data-status="done"]')).toHaveCount(0)
  items[100].message = '最终结果已写入'
  run.status = 'completed'
  await expect(body).toContainText('最终结果已写入', { timeout: 10000 })
  await expect(dialog.locator('.detail-header-status')).toHaveText('已完成', { timeout: 10000 })
  await page.clock.install()
  await page.clock.runFor(1000)
  const stopped = count
  await page.clock.runFor(10000)
  expect(count).toBe(stopped)
  const toggle = dialog.getByRole('button', { name: /^运行记录/ })
  await toggle.click()
  await toggle.click()
  await expect.poll(() => count).toBe(stopped + 1)
})

test('批次状态轮询不打断慢请求，收起记录取消请求并停止读取', async ({ page }) => {
  const run = sampleRun()
  let count = 0
  let release!: () => void
  const pending = new Promise<void>((r) => (release = r))
  await page.route('**/api/runs/960/items?*', async (route) => {
    const n = ++count
    if (n === 1) await pending
    try {
      await route.fulfill({
        json: recordPage(records(), new URL(route.request().url()).searchParams)
      })
    } catch {}
  })
  const dialog = await open(page, run),
    toggle = dialog.getByRole('button', { name: /^运行记录/ })
  await expect.poll(() => count).toBe(1)
  run.status = 'paused'
  await expect(dialog.locator('.detail-header-status')).toHaveText('已暂停', { timeout: 10000 })
  expect(count).toBe(1)
  await toggle.click()
  release()
  await page.clock.install()
  await page.clock.runFor(10000)
  expect(count).toBe(1)
  await toggle.click()
  await expect(dialog.locator('.detail-record-file')).toHaveCount(100)
  expect(count).toBe(2)
})

test('多目录同名文件与未提交名称不会被混淆', async ({ page }) => {
  const run = { ...sampleRun(), status: 'completed', discovered: 3, done: 1, failed: 2 }
  const items = [
    {
      id: 1,
      entry: 1,
      status: 'done',
      original_path: '上海 / clip.mp4',
      current_path: '其他 / 后续名称.mp4',
      new_name: '本批次名称.mp4',
      operation_status: 'committed',
      undone: true
    },
    {
      id: 2,
      entry: 2,
      status: 'failed',
      original_path: '杭州 / clip.mp4',
      current_path: '杭州 / clip.mp4',
      new_name: '占用名称.mp4',
      operation_status: 'failed',
      message: '目标名称已存在'
    },
    {
      id: 3,
      entry: 3,
      status: 'failed',
      original_path: '杭州 / 待核对.mp4',
      current_path: '杭州 / 不确定.mp4',
      new_name: '拟用名称.mp4',
      operation_status: 'review',
      message: '文件可能已改名，需核对'
    }
  ]
  await page.route('**/api/runs/960/items?*', (r) =>
    r.fulfill({ json: recordPage(items, new URL(r.request().url()).searchParams) })
  )
  const dialog = await open(page, run)
  const groups = dialog.locator('.detail-directory')
  await expect(groups).toHaveCount(2)
  await expect(groups.nth(0)).toContainText('上海')
  await expect(groups.nth(0)).toContainText('clip.mp4')
  await expect(groups.nth(0)).toContainText('本批次名称.mp4')
  await expect(groups.nth(0)).not.toContainText('后续名称.mp4')
  await expect(groups.nth(0)).toContainText('本次改名已还原')
  await expect(groups.nth(1)).toContainText('杭州')
  await expect(groups.nth(1)).toContainText('clip.mp4')
  await expect(groups.nth(1)).toContainText('拟用名称 · 改名失败')
  await expect(groups.nth(1)).toContainText('拟用名称 · 待核对')
  await groups.nth(0).scrollIntoViewIfNeeded()
  await page.screenshot({ path: 'test-results/run-directory-names-desktop.png' })
  await page.setViewportSize({ width: 390, height: 844 })
  await groups.nth(1).scrollIntoViewIfNeeded()
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBeTruthy()
  await page.screenshot({ path: 'test-results/run-directory-names-mobile.png' })
})
