import { test, expect, type Page } from '@playwright/test'
import { mockRunPages } from './run-pages'
import {
  attempts,
  directories,
  directoryToggles,
  fileRecords,
  recentFiles,
  recordPage
} from './run-record-pages'
import { defined, pageReadout } from './support'

const sampleRun = () => ({
  id: 960,
  task: 'rename',
  source: 'manual',
  status: 'running',
  targets: [1],
  target_names: ['素材'],
  created: '2026-10-07T00:00:00.000Z',
  discovered: 205,
  done: 100,
  skipped: 0,
  failed: 104
})
// The first 100 records are done, the last one queued and the rest failed.
function recordStatus(i: number) {
  if (i === 204) return 'queued'
  return i < 100 ? 'done' : 'failed'
}
const records = () =>
  Array.from({ length: 205 }, (_, i) => ({
    id: i + 1,
    entry: i + 1,
    status: recordStatus(i),
    original_path: `${i < 103 ? '上海' : '杭州'} / clip-${String(i + 1).padStart(3, '0')}.mp4`,
    current_path: '当前目录 / 当前文件.mp4',
    new_name: i < 100 ? `新名称-${i}.mp4` : '',
    operation_status: i < 100 ? 'committed' : '',
    message: i < 100 ? '' : 'rename_failed',
    updated_ms: 1791331200000,
    attempts: Array.from({ length: 4 }, (_, n) => ({
      number: n + 1,
      model_config_title: '测试模型',
      model: n === 3 ? 'fallback' : 'vision',
      fallback: n === 3,
      started: '2026-10-07T00:00:00.000Z',
      duration_ms: 500,
      status: i < 100 && n === 3 ? 'success' : 'failed',
      error: { code: 'api_http_error', details: { http_status: 500 + n } }
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
      ? route.fulfill({
          status: 503,
          json: {
            error: {
              code: 'internal',
              message: 'test failure',
              details: { detail: '暂时不可用' }
            }
          }
        })
      : route.fulfill({ json: recordPage(items, query) })
  })
  const dialog = await open(page, run),
    body = dialog.getByRole('region', { name: '运行记录', exact: true })
  await expect(body.getByRole('button', { name: /^处理中/ })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await body.getByRole('button', { name: /^全部文件/ }).click()
  await expect(fileRecords(body)).toHaveCount(100)
  await expect(dialog.getByRole('button', { name: /^运行记录/ })).toContainText(
    '205 个文件 · 2 个目录'
  )
  await expect(directoryToggles(body).first()).toContainText('103 个文件')
  await expect(body).toContainText('本页 100 / 103')
  const attemptToggle = body.getByRole('button', { name: /^全部 \d+ 次尝试/ }).first()
  await attemptToggle.click()
  await expect(attempts(body)).toHaveCount(4)
  const content = dialog.locator('.modal-content')
  await content.evaluate((el) => (el.scrollTop = 250))
  const before = requests.length
  await expect.poll(() => requests.length, { timeout: 10000 }).toBeGreaterThan(before)
  await expect(attemptToggle).toHaveAttribute('aria-expanded', 'true')
  expect(await content.evaluate((el) => el.scrollTop)).toBeCloseTo(250, 0)
  await body.getByRole('button', { name: '下页文件' }).click()
  await expect(directoryToggles(body)).toHaveCount(2)
  await expect(pageReadout(body.getByRole('navigation', { name: '运行记录翻页' }))).toHaveText(
    /^2\//
  )
  await body.getByRole('button', { name: '下页文件' }).click()
  await expect(fileRecords(body)).toHaveCount(5)
  await expect(body).toContainText('等待处理')
  await body.getByRole('button', { name: /^最终失败/ }).click()
  await expect(pageReadout(body.getByRole('navigation', { name: '运行记录翻页' }))).toHaveText(
    /^1\//
  )
  await expect(body.getByLabel('运行记录分页', { exact: true })).toContainText('共 104 个文件')
  await body.getByRole('combobox', { name: '记录目录筛选' }).click()
  await page.getByRole('option', { name: '上海（103）', exact: true }).click()
  await expect(fileRecords(body)).toHaveCount(3)
  const failed = fileRecords(body).first()
  await failed.getByRole('button', { name: /^全部 \d+ 次尝试/ }).click()
  await expect(attempts(failed)).toHaveCount(4)
  await expect(failed).toContainText('重试第 3 次 · 兜底模型')
  await expect(failed).toContainText('未生成有效名称')
  await directoryToggles(body).click()
  await expect(directoryToggles(body)).toHaveAttribute('aria-expanded', 'false')
  const collapsed = requests.length
  await expect.poll(() => requests.length, { timeout: 10000 }).toBeGreaterThan(collapsed)
  await expect(directoryToggles(body)).toHaveAttribute('aria-expanded', 'false')
  await directoryToggles(body).click()
  fail = true
  await expect(body.getByRole('alert')).toContainText('保留上次读取', { timeout: 10000 })
  await expect(fileRecords(body)).toHaveCount(3)
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
      ? route.fulfill({
          status: 503,
          json: {
            error: {
              code: 'internal',
              message: 'test failure',
              details: { detail: '读取失败' }
            }
          }
        })
      : route.fulfill({ json: recordPage([], new URL(route.request().url()).searchParams) })
  })
  const dialog = await open(page, run),
    body = dialog.getByRole('region', { name: '运行记录', exact: true })
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
    body = dialog.getByRole('region', { name: '运行记录', exact: true })
  await expect.poll(() => count).toBe(1)
  await body.getByRole('button', { name: /^最终失败/ }).click()
  await expect(body.getByLabel('运行记录分页', { exact: true })).toContainText('共 104 个文件')
  release()
  await expect(fileRecords(body).filter({ hasText: '已完成' })).toHaveCount(0)
  defined(items[100]).message = 'rename_conflict'
  run.status = 'completed'
  await expect(body).toContainText('目标已存在', { timeout: 10000 })
  await expect(dialog.getByRole('status', { name: '批次状态' })).toHaveText('已完成', {
    timeout: 10000
  })
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
    } catch {
      // The page may have closed the stale request before it was released.
    }
  })
  const dialog = await open(page, run),
    toggle = dialog.getByRole('button', { name: /^运行记录/ })
  await expect.poll(() => count).toBe(1)
  run.status = 'paused'
  await expect(dialog.getByRole('status', { name: '批次状态' })).toHaveText('已暂停', {
    timeout: 10000
  })
  expect(count).toBe(1)
  await toggle.click()
  release()
  await page.clock.install()
  await page.clock.runFor(10000)
  expect(count).toBe(1)
  await toggle.click()
  await expect(recentFiles(dialog)).toHaveCount(20)
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
      message: 'rename_conflict'
    },
    {
      id: 3,
      entry: 3,
      status: 'failed',
      original_path: '杭州 / 待核对.mp4',
      current_path: '杭州 / 不确定.mp4',
      new_name: '拟用名称.mp4',
      operation_status: 'review',
      message: 'rename_evidence_mismatch'
    }
  ]
  await page.route('**/api/runs/960/items?*', (r) =>
    r.fulfill({ json: recordPage(items, new URL(r.request().url()).searchParams) })
  )
  const dialog = await open(page, run)
  const groups = directories(dialog)
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

test('处理中视图按用时排列阶段时间轴，并显示拟用名称、最近结束与跳过原因', async ({ page }) => {
  const now = Date.now()
  const run = { ...sampleRun(), discovered: 7, done: 2, skipped: 2, failed: 1 }
  const attempt = (status: string, message = '') => ({
    number: 1,
    model_config_title: '测试模型',
    model: 'vision',
    fallback: false,
    started: new Date(now - 90000).toISOString(),
    duration_ms: 60000,
    status,
    error: message ? { code: message } : undefined
  })
  const items = [
    {
      id: 1,
      entry: 1,
      status: 'running',
      original_path: '综艺 / 第三季 / EP07_raw.mkv',
      updated_ms: now - 18000,
      stage: {
        stage: 'committing' as const,
        since_ms: now - 1000,
        attempt: 1,
        fallback: false,
        frames: 6,
        title: '嘉宾雨中拆盲盒.mkv'
      },
      attempts: [attempt('success')]
    },
    {
      id: 2,
      entry: 2,
      status: 'running',
      original_path: `电影 / 2024 / ${'超长原始文件名'.repeat(12)}.mp4`,
      updated_ms: now - 102000,
      stage: {
        stage: 'model' as const,
        since_ms: now - 12000,
        attempt: 2,
        fallback: false,
        frames: 6,
        title: ''
      },
      attempts: [attempt('failed', 'api_timeout')]
    },
    {
      id: 3,
      entry: 3,
      status: 'done',
      original_path: '电影 / DSC0042.mp4',
      new_name: '海边日落延时.mp4',
      operation_status: 'committed',
      updated_ms: now - 5000
    },
    {
      id: 4,
      entry: 4,
      status: 'skipped',
      original_path: '电影 / 旧片头.mp4',
      message: 'skip_processed',
      updated_ms: now - 7000
    },
    {
      id: 5,
      entry: 5,
      status: 'skipped',
      original_path: '综艺 / 花絮.mp4',
      message: 'skip_processed',
      updated_ms: now - 8000
    },
    {
      id: 6,
      entry: 6,
      status: 'failed',
      original_path: '综艺 / clip_009.mov',
      message: 'title_invalid',
      updated_ms: now - 9000,
      attempts: [attempt('failed'), { ...attempt('failed'), number: 2 }]
    },
    {
      id: 7,
      entry: 7,
      status: 'done',
      original_path: '电影 / 已完成.mp4',
      new_name: '新片名.mp4',
      operation_status: 'committed',
      updated_ms: now - 30000
    }
  ]
  const statuses: string[] = []
  await page.route('**/api/runs/960/items?*', (route) => {
    const query = new URL(route.request().url()).searchParams
    statuses.push(query.get('status') || 'all')
    return route.fulfill({ json: recordPage(items, query) })
  })
  const dialog = await open(page, run),
    body = dialog.getByRole('region', { name: '运行记录', exact: true })
  const running = body.getByRole('button', { name: /^处理中/ })
  await expect(running).toHaveAttribute('aria-pressed', 'true')
  await expect(running).toContainText('2')
  expect(statuses[0]).toBe('running')
  const cards = body.getByRole('article', { name: /^处理中 / })
  await expect(cards).toHaveCount(2)
  const slow = cards.first()
  await expect(slow).toContainText('电影 / 2024')
  await expect(slow).toContainText('已用时 1:4')
  await expect(slow).toContainText('等待模型生成…')
  await expect(slow.getByRole('listitem', { name: /当前阶段$/ })).toHaveText('重试第 1 次')
  await expect(slow.getByRole('listitem', { name: /已完成$/ })).toHaveText(['开始', '抽帧 6 张'])
  await expect(slow).toContainText('首次请求失败：模型请求或响应读取超时')
  const committing = cards.nth(1)
  await expect(committing).toContainText('拟用名称')
  await expect(committing).toContainText('嘉宾雨中拆盲盒.mkv')
  await expect(committing.getByRole('listitem', { name: /当前阶段$/ })).toHaveText('提交改名中')
  const recent = recentFiles(body)
  await expect(recent).toHaveCount(5)
  await expect(recent.first()).toContainText('DSC0042.mp4')
  await expect(recent.first()).toContainText('海边日落延时.mp4')
  await expect(recent.first()).toContainText('已完成 · 用时 0:24')
  await expect(recent.nth(1)).toContainText('已跳过 · 用时 0:24 · 已处理，跳过')
  await expect(recent.nth(1)).toContainText('保留原名')
  await expect(recent.nth(3)).toContainText('最终失败 · 用时 0:24 · 2 次尝试')
  await expect(recent.nth(3)).toContainText('清洗后的标题无效')
  await slow.scrollIntoViewIfNeeded()
  await dialog.screenshot({ path: 'test-results/run-live-desktop.png', animations: 'disabled' })

  await body
    .getByRole('region', { name: /^最近结束/ })
    .screenshot({ path: 'test-results/run-live-recent.png' })
  await body.getByRole('button', { name: /^已跳过/ }).click()
  await expect(body.getByRole('list', { name: '跳过原因' })).toHaveText('已处理，跳过 2')
  await expect(fileRecords(body)).toHaveCount(2)
  await expect(body).toContainText('本页 1 / 1 个跳过文件')
  await body.getByRole('button', { name: /^已完成/ }).click()
  await expect(fileRecords(body)).toHaveCount(2)
  await expect(body).toContainText('海边日落延时.mp4')
  await expect(body.getByRole('list', { name: '跳过原因' })).toHaveCount(0)
  await body.getByRole('combobox', { name: '记录目录筛选' }).click()
  await page.getByRole('option', { name: '综艺（2）', exact: true }).click()
  await expect(body.getByRole('button', { name: /^已完成/ })).toContainText('0')
  await expect(body.getByRole('status')).toHaveText('当前范围没有已完成的文件。')
  await body.getByRole('button', { name: /^处理中/ }).focus()
  await page.keyboard.press('Enter')
  await expect(cards).toHaveCount(0)
  await expect(body.getByRole('article', { name: /^处理中 / })).toHaveCount(0)
  await expect(body.getByRole('status')).toHaveCount(1)
  await expect(recentFiles(body)).toHaveCount(2)
  await body.getByRole('combobox', { name: '记录目录筛选' }).click()
  await page.getByRole('option', { name: /^全部目录/ }).click()
  await expect(body.getByRole('article', { name: /^处理中 / })).toHaveCount(2)
  await page.setViewportSize({ width: 320, height: 844 })
  await slow.scrollIntoViewIfNeeded()
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBeTruthy()
  expect(await body.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBeTruthy()
  await page.screenshot({ path: 'test-results/run-live-320.png' })
})
