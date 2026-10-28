import { defined, toasts } from './support'
import { test, expect, type Page } from '@playwright/test'
import { mockRunPages } from './run-pages'
import { attempts, fileRecords, recordPage } from './run-record-pages'

const sampleRun = () => ({
  id: 146,
  task: 'rename',
  source: 'manual',
  plan: 0,
  status: 'running',
  targets: [1],
  target_names: ['color1 / ts'],
  recursive: true,
  done: 1887,
  skipped: 108,
  failed: 1,
  discovered: 1996,
  created: '2026-10-03T10:48:45.000Z',
  started: '2026-10-03T10:48:46.000Z',
  finished: '',
  model_config_id: 1,
  model_config_title: 'zcb',
  prompt_body: '准确描述画面，只输出文件标题。\n' + '长提示词内容'.repeat(100),
  request_config: {
    model: 'vision-main',
    retry_enabled: true,
    retry_count: 3,
    fallback_model_title: '5060ti'
  },
  token_count: 0
})
const sampleItems = () => [
  {
    id: 1,
    entry: 1,
    status: 'running',
    original_path: `color1 / ts / ${'长文件名'.repeat(40)}.mp4`,
    current_path: `/path/to/current/${'长路径'.repeat(80)}/xxxxx.mp4`,
    message: '',
    attempts: []
  },
  {
    id: 2,
    entry: 2,
    status: 'done',
    original_path: 'color1 / ts / 原文件.mp4',
    current_path: 'color1 / ts / 后续名称.mp4',
    new_name: '新文件名.mp4',
    operation_status: 'committed',
    message: '',
    attempts: [
      {
        number: 1,
        model_config_title: '主模型配置',
        model: 'vision-main',
        fallback: false,
        started: '2026-10-03T10:51:32.000Z',
        duration_ms: 12300,
        status: 'success'
      }
    ]
  },
  {
    id: 3,
    entry: 3,
    status: 'failed',
    original_path: 'color1 / ts / 失败文件.mp4',
    current_path: 'color1 / ts / 失败文件.mp4',
    message: 'api_timeout',
    attempts: Array.from({ length: 4 }, (_, i) => ({
      number: i + 1,
      model_config_title: i ? '兜底配置' : '主模型配置',
      model: i ? '5060ti' : 'vision-main',
      fallback: i > 0,
      started: '2026-10-03T12:31:22.000Z',
      duration_ms: 12000,
      status: 'failed',
      error: { code: 'api_timeout' }
    }))
  }
]
async function expectEvenTimeline(page: Page) {
  const nodes = await page
    .getByRole('list', { name: '批次生命周期' })
    .getByRole('listitem')
    .evaluateAll((items) =>
      items.map((item) => {
        const cell = item.getBoundingClientRect(),
          node = item.querySelector('.lifecycle-node')?.getBoundingClientRect()
        return {
          center: node ? node.x + node.width / 2 : NaN,
          cellCenter: cell.x + cell.width / 2,
          width: cell.width
        }
      })
    )
  expect(nodes).toHaveLength(4)
  for (const node of nodes) expect(Math.abs(node.center - node.cellCenter)).toBeLessThan(1)
  for (let i = 1; i < nodes.length; i++)
    expect(defined(nodes[i]).center - defined(nodes[i - 1]).center).toBeCloseTo(
      defined(nodes[0]).width,
      0
    )
}
async function login(page: Page) {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
}
// The extraction sample omits the model request configuration.
type SampleRun = Omit<ReturnType<typeof sampleRun>, 'request_config'> & {
  request_config?: ReturnType<typeof sampleRun>['request_config']
}

async function openSample(page: Page, run: SampleRun = sampleRun()) {
  await page.clock.setFixedTime(new Date('2026-10-03T13:02:10.000Z'))
  await mockRunPages(page, [run])
  await page.route('**/api/runs/146', (route) => route.fulfill({ json: run }))
  await page.route('**/api/runs/146/items?*', (route) => {
    return route.fulfill({
      json: recordPage(sampleItems(), new URL(route.request().url()).searchParams)
    })
  })
  await login(page)
  await page.getByRole('button', { name: '批次记录', exact: true }).click()
  await page.getByRole('button', { name: '详情', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '运行 #146' })
  // Active batches open on the files being processed; the extract timeline has no model step.
  await expect(dialog.getByRole('button', { name: /^处理中/ })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await expect(dialog.getByRole('article', { name: /^处理中 / })).toHaveCount(1)
  await expect(dialog.getByRole('list', { name: '处理阶段' }).getByRole('listitem')).toHaveCount(
    run.task === 'extract' ? 3 : 5
  )
  await dialog.getByRole('button', { name: /^全部文件/ }).click()
  await expect(fileRecords(dialog)).toHaveCount(3)
  return dialog
}

test('时间线详情示例、模型展开、复制、移动布局和实时刷新', async ({ page, context }) => {
  const run = sampleRun()
  const dialog = await openSample(page, run)
  await expect(dialog.getByText(/耗时 2h13m24s$/)).toBeVisible()
  const statistics = dialog.getByRole('list', { name: '批次统计' }).getByRole('listitem')
  await expect(statistics).toHaveText([/^1,887已完成/, /^108已跳过/, /^1失败/, /^1,996总任务/])
  await expect(statistics.first()).toHaveText(/94\.5%$/)
  await expect(dialog.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '100')
  for (const [label, expanded] of [
    ['运行记录', 'true'],
    ['批次配置与提交范围', 'false']
  ] as const) {
    await expect(dialog.getByRole('button', { name: new RegExp(`^${label}`) })).toHaveAttribute(
      'aria-expanded',
      expanded
    )
  }
  await expect(
    dialog.getByRole('list', { name: '批次生命周期' }).getByRole('listitem', { name: /当前阶段$/ })
  ).toContainText('处理中')
  await expect(dialog.getByRole('group', { name: '批次操作' }).getByRole('button')).toHaveText([
    '暂停',
    '取消'
  ])
  await expect(dialog.getByRole('button', { name: '关闭', exact: true })).toHaveCount(1)
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await dialog
    .getByRole('button', { name: `复制当前路径 ${'长文件名'.repeat(40)}.mp4`, exact: true })
    .click()
  await expect(toasts(page).first()).toContainText('路径已复制')
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    defined(sampleItems()[0]).current_path
  )
  await page.setViewportSize({ width: 1440, height: 1200 })
  await expectEvenTimeline(page)
  await page.screenshot({
    path: 'test-results/run-detail-timeline-desktop.png',
    animations: 'disabled'
  })
  await dialog
    .getByRole('list', { name: '批次生命周期' })
    .screenshot({ path: 'test-results/run-detail-lifecycle-running.png', animations: 'disabled' })
  await page.setViewportSize({ width: 390, height: 844 })
  await expectEvenTimeline(page)
  await page.screenshot({
    path: 'test-results/run-detail-timeline-mobile.png',
    animations: 'disabled'
  })
  await page.setViewportSize({ width: 1440, height: 1200 })
  await dialog
    .getByRole('button', { name: /^全部 \d+ 次尝试/ })
    .first()
    .click()
  await expect(attempts(dialog).first()).toContainText('12300 ms')
  await expect(attempts(dialog).first()).toContainText('vision-main')
  await dialog
    .getByRole('button', { name: /^全部 \d+ 次尝试/ })
    .last()
    .click()
  const failed = dialog.getByRole('article', { name: '文件记录 失败文件.mp4' })
  await expect(attempts(failed)).toHaveCount(4)
  await expect(failed).toContainText('重试第 3 次')
  await expect(failed).toContainText('5060ti')
  await expect(fileRecords(dialog).nth(1)).toContainText('新文件名.mp4')
  await expect(fileRecords(dialog).nth(1)).not.toContainText('后续名称.mp4')
  // A polling update must keep user-controlled expansion and request details open.
  run.status = 'paused'
  await expect(dialog.getByRole('status', { name: '批次状态' })).toHaveText('已暂停', {
    timeout: 10000
  })
  await expect(dialog.getByRole('group', { name: '批次操作' }).getByRole('button')).toHaveText([
    '继续',
    '取消'
  ])
  await expect(dialog.getByRole('button', { name: /^运行记录/ })).toHaveAttribute(
    'aria-expanded',
    'true'
  )
  await expect(dialog.getByRole('button', { name: /^全部 \d+ 次尝试/ }).first()).toHaveAttribute(
    'aria-expanded',
    'true'
  )
  await expect(
    dialog.getByRole('list', { name: '批次生命周期' }).getByRole('listitem', { name: /当前阶段$/ })
  ).toHaveAttribute('data-tone', 'warning')
  await dialog.getByRole('button', { name: /^批次配置与提交范围/ }).click()
  await expect(dialog.getByRole('region', { name: '批次配置与提交范围' })).toContainText(
    'vision-main'
  )
  await dialog.getByRole('button', { name: /^提示词快照/ }).click()
  await expect(dialog.getByRole('region', { name: '提示词快照' })).toContainText('长提示词内容')
  await page.setViewportSize({ width: 390, height: 844 })
  await expectEvenTimeline(page)
  const overflow = await dialog.evaluate((el) =>
    Array.from(
      el.querySelectorAll<HTMLElement>(
        '.modal-content,.detail-prompt-body,.detail-directory-files,.detail-record-names'
      )
    ).some((node) => node.scrollWidth > node.clientWidth)
  )
  expect(overflow).toBe(false)
  const header = await dialog.locator(':scope > header').boundingBox()
  await dialog.locator('.modal-content').evaluate((el) => {
    el.scrollTop = el.scrollHeight
  })
  expect(defined(await dialog.locator(':scope > header').boundingBox()).y).toBe(defined(header).y)
  await dialog.locator('.modal-content').evaluate((el) => {
    el.scrollTop = 0
  })
  await page.screenshot({
    path: 'test-results/run-detail-timeline-mobile-expanded.png',
    animations: 'disabled'
  })
  await dialog.getByRole('button', { name: '关闭', exact: true }).first().focus()
  await page.keyboard.press('Shift+Tab')
  await expect(
    dialog.getByRole('group', { name: '批次操作' }).getByRole('button').last()
  ).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
})

test('失败、取消、完成与未开始批次的生命周期', async ({ page }) => {
  const run = sampleRun()
  const dialog = await openSample(page, run)
  for (const status of [
    'completed',
    'failed',
    'partial_failure',
    'interrupted',
    'cancelled',
    'queued',
    'scanning'
  ]) {
    run.status = status
    run.started = ['queued', 'scanning', 'cancelled'].includes(status)
      ? ''
      : '2026-10-03T10:48:46.000Z'
    run.finished = ['queued', 'scanning'].includes(status) ? '' : '2026-10-03T13:02:10.000Z'
    await expect(dialog.getByRole('status', { name: '批次状态' })).toHaveAttribute(
      'data-status',
      status,
      {
        timeout: 10000
      }
    )
    const terminal = dialog.getByRole('list', { name: '批次生命周期' }).getByRole('listitem').last()
    const terminalTones: Record<string, string> = {
      completed: 'success',
      cancelled: 'muted',
      queued: 'pending',
      scanning: 'pending'
    }
    await expect(terminal).toHaveAttribute('data-tone', terminalTones[status] ?? 'error')
    if (['completed', 'failed', 'cancelled'].includes(status))
      await dialog.getByRole('list', { name: '批次生命周期' }).screenshot({
        path: `test-results/run-detail-lifecycle-${status}.png`,
        animations: 'disabled'
      })
    if (status === 'scanning')
      await expect(
        dialog
          .getByRole('list', { name: '批次生命周期' })
          .getByRole('listitem', { name: /当前阶段$/ })
      ).toHaveAttribute('data-tone', 'primary')
    if (['failed', 'partial_failure', 'interrupted', 'cancelled'].includes(status)) {
      await expect(terminal).not.toHaveText(/已完成/)
    }
  }
})

test('独立抽帧详情不展示命名配置，保留抽帧文件与批次控制', async ({ page }) => {
  const run = {
    ...sampleRun(),
    task: 'extract',
    status: 'paused',
    prompt_body: '',
    request_config: undefined,
    model_config_id: 0,
    model_config_title: ''
  }
  const dialog = await openSample(page, run)
  await expect(dialog.getByText('独立抽帧 · 不使用模型', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: /^提示词快照/ })).toHaveCount(0)
  await expect(fileRecords(dialog)).toHaveCount(3)
  await dialog.getByRole('button', { name: /^批次配置与提交范围/ }).click()
  await expect(dialog.getByRole('region', { name: '批次配置与提交范围' })).toContainText('独立抽帧')
  await expect(dialog.getByRole('region', { name: '批次配置与提交范围' })).not.toContainText(
    '主模型'
  )
  await expect(dialog.getByRole('button', { name: '继续', exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: '取消', exact: true })).toBeVisible()
})
