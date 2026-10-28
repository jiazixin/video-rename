import { bodyRows, libraryDirectory } from './support'
import { test, expect, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'

async function login(page: Page) {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
}
async function submitRun(page: Page, button = '抽帧') {
  const response = page.waitForResponse(
    (r) => r.url().endsWith('/api/runs') && r.request().method() === 'POST'
  )
  await page.getByRole('dialog').getByRole('button', { name: button, exact: true }).click()
  const result = await response
  expect(result.ok()).toBeTruthy()
  const { id } = await result.json()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect
    .poll(async () => (await (await page.request.get(`/api/runs/${id}`)).json()).status, {
      timeout: 30000
    })
    .toMatch(/^(completed|partial_failure)$/)
  return id as number
}

test('三个独立抽帧入口、真实缓存复用及统一成功失败记录', async ({ page }) => {
  await login(page)
  const { settings } = await (await page.request.get('/api/system/settings')).json()
  expect(
    (
      await page.request.put('/api/system/settings', {
        data: { ...settings, stability_seconds: 0 }
      })
    ).ok()
  ).toBeTruthy()
  try {
    await page.getByRole('button', { name: '独立抽帧测试', exact: true }).click()
    await expect(page.getByText('prepared.mp4', { exact: true })).toBeVisible()
    const directory = libraryDirectory(page.url())
    const entries = await (
      await page.request.get(`/api/directories/${directory}/entries?limit=100`)
    ).json()
    const file = entries.items.find((e: Record<string, unknown>) => e.name === 'prepared.mp4')
    expect(
      (
        await page.request.post('/api/entries/processed', {
          data: { targets: [file.id], marked: true }
        })
      ).ok()
    ).toBeTruthy()
    const extract = page
      .getByRole('group', { name: '当前目录操作' })
      .getByRole('button', { name: '抽帧当前目录', exact: true })
    expect(await extract.evaluate((el) => el.nextElementSibling?.textContent.trim())).toBe('重命名')
    // A visible search filter must not restrict a directory extraction.
    await page.getByLabel('搜索当前目录').fill('prepared')
    await expect(bodyRows(page)).toHaveCount(1)
    await extract.click()
    await expect(page.getByRole('dialog').getByRole('combobox', { name: '模型配置' })).toHaveCount(
      0
    )
    await expect(page.getByLabel('包含子目录', { exact: true })).toBeChecked()
    await page.getByLabel('包含子目录', { exact: true }).uncheck()
    const first = await submitRun(page)
    const firstRun = await (await page.request.get(`/api/runs/${first}`)).json()
    expect(firstRun).toMatchObject({
      task: 'extract',
      done: 1,
      failed: 1,
      discovered: 2,
      model_config_id: 0,
      token_records: 0
    })
    const firstLog = await (await page.request.get(`/api/extractions?run=${first}`)).json()
    expect(firstLog.success.items[0]).toMatchObject({
      source: 'manual',
      cached: false,
      entry: file.id
    })
    expect(firstLog.success.items[0].frames).toBeGreaterThan(0)
    expect(firstLog.failure.items[0].error.code).toBeTruthy()
    const current = (await (await page.request.get(`/api/entries/status?ids=${file.id}`)).json())
      .items
    expect(current[0]).toMatchObject({ name: 'prepared.mp4', processed: 'marked' })
    expect(current[0].restore_id || 0).toBe(0)

    await page.getByRole('button', { name: '刷新', exact: true }).click()
    await page.getByRole('button', { name: '多选', exact: true }).click()
    await expect(page.getByRole('group', { name: '批量操作' }).getByRole('button')).toHaveText([
      '全选',
      '反选',
      '清空',
      '删除',
      '抽帧',
      '命名',
      '标记已处理',
      '取消标记',
      '还原'
    ])
    await page.getByLabel('选择 prepared.mp4', { exact: true }).check()
    await page
      .getByRole('group', { name: '批量操作' })
      .getByRole('button', { name: '抽帧', exact: true })
      .click()
    const second = await submitRun(page)
    const secondLog = await (await page.request.get(`/api/extractions?run=${second}`)).json()
    expect(secondLog.success.items).toHaveLength(1)
    expect(secondLog.success.items[0].cached).toBe(true)
    await page.screenshot({ path: 'test-results/extraction-library.png', fullPage: true })

    await page.getByRole('button', { name: '批次记录', exact: true }).click()
    await expect(page.getByRole('button', { name: '新建命名任务', exact: true })).toHaveCount(0)
    await page.getByRole('button', { name: '计划管理', exact: true }).click()
    await page.getByRole('button', { name: '新建手动计划', exact: true }).click()
    await page.getByRole('combobox', { name: '计划任务类型' }).click()
    await page.getByRole('option', { name: '抽帧', exact: true }).click()
    await expect(page.getByRole('dialog', { name: '新建手动计划' })).toBeVisible()
    await expect(page.getByRole('dialog').getByRole('combobox', { name: '模型配置' })).toHaveCount(
      0
    )
    await page.getByLabel('选择 独立抽帧测试', { exact: true }).check()
    await page.setViewportSize({ width: 390, height: 844 })
    expect(
      await page.getByRole('dialog').evaluate((el) => el.scrollWidth <= el.clientWidth)
    ).toBeTruthy()
    await page.screenshot({ path: 'test-results/extraction-dialog-mobile.png', fullPage: true })
    await page.setViewportSize({ width: 1440, height: 1000 })
    const third = await submitRun(page, '立即运行')
    await page.getByRole('button', { name: '批次记录', exact: true }).click()
    const thirdRun = await (await page.request.get(`/api/runs/${third}`)).json()
    expect(thirdRun).toMatchObject({ task: 'extract', done: 2, failed: 1, discovered: 3 })
    const runRow = page.getByRole('article', { name: `批次 #${third}`, exact: true })
    await expect(runRow).toContainText('抽帧')
    await expect(runRow.getByRole('button', { name: '还原批次', exact: true })).toHaveCount(0)

    await page.getByRole('button', { name: '计划管理', exact: true }).click()
    await page.getByRole('button', { name: '新建自动计划', exact: true }).click()
    await page.getByRole('combobox', { name: '计划任务类型' }).click()
    await page.getByRole('option', { name: '抽帧', exact: true }).click()
    await page.getByLabel('计划名称', { exact: true }).fill('定时抽帧测试')
    await page.getByLabel('选择 独立抽帧测试', { exact: true }).check()
    await page.getByLabel('保存后启用计划', { exact: true }).uncheck()
    await page.getByRole('button', { name: '保存计划', exact: true }).click()
    await page.reload()
    const card = page.getByRole('article', { name: '定时抽帧测试', exact: true })
    await expect(card).toContainText('抽帧')
    await page.getByRole('button', { name: '编辑 定时抽帧测试' }).click()
    await expect(page.getByRole('combobox', { name: '计划任务类型' })).toHaveText('抽帧')
    await expect(page.getByRole('dialog').getByRole('combobox', { name: '模型配置' })).toHaveCount(
      0
    )
    await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
    const scheduledResponse = page.waitForResponse((r) =>
      /\/api\/schedules\/\d+\/run$/.test(r.url())
    )
    await card.getByRole('button', { name: '立即运行', exact: true }).click()
    const scheduled = await scheduledResponse
    expect(scheduled.ok()).toBeTruthy()
    const scheduledRun = await scheduled.json()
    await expect
      .poll(
        async () => (await (await page.request.get(`/api/runs/${scheduledRun.id}`)).json()).status,
        { timeout: 30000 }
      )
      .toBe('partial_failure')
    expect(await (await page.request.get(`/api/runs/${scheduledRun.id}`)).json()).toMatchObject({
      task: 'extract',
      model_config_id: 0
    })
    await page.getByRole('button', { name: '删除 定时抽帧测试' }).click()
    await page.getByRole('button', { name: '删除计划', exact: true }).click()

    // Naming the same video must consume the retained frames, without another FFmpeg pass.
    expect(
      (
        await page.request.post('/api/entries/processed', {
          data: { targets: [file.id], marked: false }
        })
      ).ok()
    ).toBeTruthy()
    const prompt = await (
      await page.request.post('/api/prompts', {
        data: { title: '抽帧链路提示词', body: '用于测试的独立提示词：只输出标题' }
      })
    ).json()
    const { modelURL } = JSON.parse(readFileSync('test-results/fixture.json', 'utf8'))
    const profileResponse = await page.request.post('/api/model-configs', {
      data: {
        title: '抽帧链路模型',
        prompt_id: prompt.id,
        provider: 'custom',
        protocol: 'openai',
        base_url: modelURL,
        model: '视觉模型 · 本地测试',
        timeout_ms: 10000,
        workers: 1,
        max_tokens: 1024,
        temperature: 0.7
      }
    })
    expect(profileResponse.ok()).toBeTruthy()
    const profile = await profileResponse.json()
    const renameResponse = await page.request.post('/api/runs', {
      headers: { 'Idempotency-Key': 'extraction-e2e-rename' },
      data: {
        task: 'rename',
        targets: [file.id],
        model_config_id: profile.id
      }
    })
    expect(renameResponse.ok()).toBeTruthy()
    const renamed = await renameResponse.json()
    await expect
      .poll(async () => (await (await page.request.get(`/api/runs/${renamed.id}`)).json()).status, {
        timeout: 30000
      })
      .toBe('completed')
    const automatic = await (await page.request.get(`/api/extractions?run=${renamed.id}`)).json()
    expect(automatic.success.items[0]).toMatchObject({ cached: true, source: 'automatic' })
    expect(automatic.success.items[0].started).toBeTruthy()
    expect(automatic.success.items[0].finished).toBeTruthy()
    expect(automatic.success.items[0].duration_ms).toBeGreaterThanOrEqual(0)

    const analytics = await (await page.request.get('/api/runs/analytics')).json()
    expect(
      analytics.batches.items.find((b: Record<string, unknown>) => b.id === first)
    ).toMatchObject({
      task: 'extract',
      profile_id: 0,
      profile_title: '',
      model: ''
    })
    expect(
      analytics.batches.items.find((b: Record<string, unknown>) => b.id === renamed.id)
    ).toMatchObject({
      task: 'rename',
      profile_id: profile.id,
      model: '视觉模型 · 本地测试'
    })
    expect(analytics.profiles.some((p: Record<string, unknown>) => p.id === 0)).toBe(false)
    expect(
      analytics.profiles.find((p: Record<string, unknown>) => p.id === profile.id)
    ).toMatchObject({
      batches: 1,
      done: 1
    })
    const extractionAnalytics = await (
      await page.request.get('/api/runs/analytics?profile=0')
    ).json()
    expect(extractionAnalytics.profiles).toEqual([])
    expect(extractionAnalytics.batches.total).toBeGreaterThanOrEqual(4)
    expect(
      extractionAnalytics.batches.items.every((b: Record<string, unknown>) => b.task === 'extract')
    ).toBe(true)

    await page.getByRole('button', { name: '抽帧记录', exact: true }).click()
    await expect(page.getByRole('group', { name: '抽帧记录状态' }).getByRole('button')).toHaveText([
      '成功记录',
      '失败记录'
    ])
    const success = page.getByRole('region', { name: '抽帧成功', exact: true }),
      failure = page.getByRole('region', { name: '抽帧失败', exact: true })
    await expect(success).toContainText('复用缓存')
    await expect(success).toContainText('自动抽帧')
    await expect(failure).toHaveCount(0)
    await page.getByRole('button', { name: '失败记录', exact: true }).click()
    await expect(failure).toContainText('broken.mp4')
    await expect(success).toHaveCount(0)
    await page.screenshot({ path: 'test-results/extraction-history.png', fullPage: true })
    await failure.getByRole('button', { name: '详细原因', exact: true }).first().click()
    await expect(page.getByRole('dialog').getByText(/moov atom not found/)).toBeVisible()
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
    await page.getByRole('button', { name: '复制详情', exact: true }).click()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(
      'moov atom not found'
    )
    await page.screenshot({ path: 'test-results/extraction-failure-detail.png', fullPage: true })
  } finally {
    expect((await page.request.put('/api/system/settings', { data: settings })).ok()).toBeTruthy()
  }
})

test('抽帧历史独立分页、无批次、窄屏与清除', async ({ page }) => {
  await login(page)
  const item = {
    id: 10,
    run: 0,
    entry: 0,
    path: '目录/无批次.mp4',
    source: 'manual',
    status: 'success',
    started: '2026-09-26T01:00:00.000Z',
    finished: '2026-09-26T01:00:02.000Z',
    duration_ms: 2000,
    cached: false,
    frames: 5
  }
  let cleared = false
  await page.route('**/api/extractions?*', (route) => {
    const params = new URL(route.request().url()).searchParams
    const second = String(Math.floor(Number(params.get('success_offset') || 0) / 50) + 1) === '2'
    return route.fulfill({
      json: {
        success: {
          items: cleared ? [] : [{ ...item, path: second ? '目录/第二页.mp4' : item.path }],
          total: cleared ? 0 : 51,
          offset: ((!cleared && second ? 2 : 1) - 1) * 50,
          limit: 50
        },
        failure: {
          items: cleared
            ? []
            : [
                {
                  ...item,
                  id: 11,
                  status: 'failure',
                  path: '目录/失败.mp4',
                  error: {
                    code: 'extract_failed',
                    message: 'extract_failed: decoder unavailable',
                    details: { detail: 'decoder unavailable' }
                  }
                }
              ],
          total: cleared ? 0 : 1,
          offset: 0,
          limit: 50
        },
        running: 0
      }
    })
  })
  await page.route('**/api/extractions/clear', (route) => {
    expect(route.request().postDataJSON()).toEqual({ all: true })
    cleared = true
    return route.fulfill({ json: { deleted: 52 } })
  })
  await page.getByRole('button', { name: '抽帧记录', exact: true }).click()
  const success = page.getByRole('region', { name: '抽帧成功', exact: true }),
    failure = page.getByRole('region', { name: '抽帧失败', exact: true })
  await expect(bodyRows(success).first().getByRole('cell').nth(3)).toHaveText('—')
  await page.getByRole('button', { name: '下一页抽帧成功', exact: true }).click()
  await expect(success).toContainText('第二页.mp4')
  await page.getByRole('button', { name: '失败记录', exact: true }).click()
  await expect(failure).toContainText('失败.mp4')
  await expect(page.getByRole('button', { name: '上一页抽帧失败', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '成功记录', exact: true }).click()
  await expect(success).toContainText('第二页.mp4')
  await page.getByRole('button', { name: '上一页抽帧成功', exact: true }).click()
  await expect(success).toContainText('无批次.mp4')
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
  await page.screenshot({ path: 'test-results/extraction-history-mobile.png', fullPage: true })
  await page.getByRole('button', { name: '清除记录', exact: true }).click()
  await page.getByRole('button', { name: '确认清除', exact: true }).click()
  await expect(success).toContainText('暂无抽帧成功记录')
  await page.getByRole('button', { name: '失败记录', exact: true }).click()
  await expect(failure).toContainText('暂无抽帧失败记录')
})
