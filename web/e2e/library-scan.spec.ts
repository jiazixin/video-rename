import { test, expect, type Page } from '@playwright/test'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

async function login(page: Page) {
  const session = await (
    await page.request.post('/api/auth/login', {
      data: { username: 'admin', password: 'test-password-123' }
    })
  ).json()
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
  return { 'X-CSRF-Token': session.csrf }
}

test('后台扫描默认一小时，范围与间隔保存后重开保持，窄屏可操作', async ({ page }) => {
  const headers = await login(page)
  const original = (await (await page.request.get('/api/library/scan')).json()).settings
  expect(original.interval_minutes).toBe(60)
  try {
    await page.getByRole('button', { name: '系统设置', exact: true }).click()
    const panel = page.locator('.library-scan-settings')
    await expect(panel.getByLabel('扫描间隔（分钟）', { exact: true })).toHaveValue('60')
    await panel.getByLabel('扫描间隔（分钟）', { exact: true }).fill('120')
    await panel.getByLabel('开启定时扫描', { exact: true }).check()
    await expect(
      panel.getByRole('button', { name: '立即扫描全部范围', exact: true })
    ).toBeDisabled()
    await panel.getByRole('button', { name: '保存扫描设置', exact: true }).click()
    await expect(panel.getByRole('status')).toContainText('扫描设置已保存')
    await page.reload()
    await expect(panel.getByLabel('扫描间隔（分钟）', { exact: true })).toHaveValue('120')
    await expect(panel.getByLabel('开启定时扫描', { exact: true })).toBeChecked()
    await expect(panel.locator('.picker-current input')).toBeChecked()
    await panel.locator('.picker-current input').uncheck()
    await expect(panel.getByRole('button', { name: '保存扫描设置', exact: true })).toBeDisabled()
    await expect(panel.getByText('请选择 1–100 个扫描目录。', { exact: true })).toBeVisible()
    await panel.locator('.picker-current input').check()
    await panel.scrollIntoViewIfNeeded()
    await page.screenshot({
      path: 'test-results/library-scan-settings-desktop.png',
      fullPage: true
    })
    await page.setViewportSize({ width: 390, height: 844 })
    await panel.scrollIntoViewIfNeeded()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: 'test-results/library-scan-settings-mobile.png', fullPage: true })
  } finally {
    expect(
      (await page.request.put('/api/library/scan/settings', { headers, data: original })).ok()
    ).toBe(true)
  }
})

test('手动递归扫描发现多个目录中新视频和新子目录，不创建处理批次', async ({ page }) => {
  const { mediaPath } = JSON.parse(readFileSync('test-results/fixture.json', 'utf8'))
  const name = '后台扫描发现测试',
    folder = join(mediaPath, name)
  mkdirSync(join(folder, '甲目录'), { recursive: true })
  mkdirSync(join(folder, '乙目录'), { recursive: true })
  const headers = await login(page)
  await page.request.post('/api/directories/1/refresh', { headers, data: {} })
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await page.getByRole('button', { name, exact: true }).click()
  await expect(page.getByRole('button', { name: '甲目录', exact: true })).toBeVisible()
  const id = Number(new URL(page.url()).hash.split('/')[1])
  writeFileSync(join(folder, '甲目录', '新视频甲.mp4'), 'scan fixture')
  writeFileSync(join(folder, '乙目录', '新视频乙.mp4'), 'scan fixture')
  mkdirSync(join(folder, '全新目录', '深层'), { recursive: true })
  writeFileSync(join(folder, '全新目录', '深层', '深层新增.mp4'), 'scan fixture')
  const beforeResponse = await page.request.get('/api/runs?category=all&page=1&size=1')
  expect(beforeResponse.ok()).toBe(true)
  const before = await beforeResponse.json()
  expect(Number.isInteger(before.total)).toBe(true)
  await page.getByRole('button', { name: '扫描当前目录及子目录', exact: true }).click()
  await expect(page.locator('.library-scan-status')).toContainText('扫描完成', { timeout: 15000 })
  await expect(page.getByRole('button', { name: '全新目录', exact: true })).toBeVisible({
    timeout: 15000
  })
  const parent = await (await page.request.get(`/api/directories/${id}/entries?page=1`)).json()
  for (const [dirName, fileName] of [
    ['甲目录', '新视频甲.mp4'],
    ['乙目录', '新视频乙.mp4']
  ]) {
    const child = parent.items.find((e: any) => e.name === dirName)
    const files = await (
      await page.request.get(`/api/directories/${child.id}/entries?page=1`)
    ).json()
    expect(files.items.map((e: any) => e.name)).toContain(fileName)
    expect(files.items.find((e: any) => e.name === fileName).processed).toBe('')
  }
  const fresh = parent.items.find((e: any) => e.name === '全新目录')
  const nested = await (
    await page.request.get(`/api/directories/${fresh.id}/entries?page=1`)
  ).json()
  const deep = nested.items.find((e: any) => e.name === '深层')
  const deepFiles = await (
    await page.request.get(`/api/directories/${deep.id}/entries?page=1`)
  ).json()
  expect(deepFiles.items.map((e: any) => e.name)).toEqual(['深层新增.mp4'])
  const afterResponse = await page.request.get('/api/runs?category=all&page=1&size=1')
  expect(afterResponse.ok()).toBe(true)
  const after = await afterResponse.json()
  expect(after.total).toBe(before.total)
})

const entry = (id: number, name: string, kind = 'file') => ({
  id,
  parent: 1,
  name,
  kind,
  size: 1,
  mtime: '2026-10-03T00:00:00Z',
  version: 1,
  processed: '',
  restore_id: 0,
  task_status: ''
})
const idleStatus = {
  id: '',
  status: 'idle',
  source: '',
  targets: [],
  started: '',
  finished: '',
  next_scan: '',
  current_path: '',
  directories: 0,
  files: 0,
  added: 0,
  changed: 0,
  removed: 0,
  errors: []
}

for (const mode of ['auto', 'selected', 'late-selection'])
  test(`后台索引更新${mode === 'auto' ? '自动显示新增文件' : mode === 'selected' ? '等待确认并保留勾选' : '忽略读取期间的新选择'}，离页停止轮询`, async ({
    page
  }) => {
    const selected = mode === 'selected',
      selectLate = mode === 'late-selection'
    let generation = '1',
      scanRequests = 0,
      stateRequests = 0,
      listRequests = 0
    let release!: () => void,
      listWaiting = false
    const delayed = new Promise<void>((resolve) => {
      release = resolve
    })
    const initial = [entry(10, '保留.mp4'), entry(11, '原有.mp4')]
    await page.route('**/api/library/scan', (route) => {
      scanRequests++
      return route.fulfill({
        json: {
          settings: { enabled: true, interval_minutes: 60, targets: [1] },
          status: idleStatus
        }
      })
    })
    await page.route('**/api/directories/1/state', (route) => {
      stateRequests++
      return route.fulfill({ json: { generation, refreshed: '', refreshing: false, error: '' } })
    })
    await page.route('**/api/directories/1/entries?*', async (route) => {
      listRequests++
      const items = generation === '1' ? initial : [...initial, entry(12, '新增.mp4')]
      if (selectLate && generation === '2') {
        listWaiting = true
        await delayed
      }
      return route.fulfill({
        json: {
          page: 1,
          total: items.length,
          items,
          next: 0,
          generation,
          refreshed: '',
          refreshing: false,
          error: '',
          directory: entry(1, '媒体库', 'directory'),
          breadcrumbs: []
        }
      })
    })
    await page.route('**/api/entries/status?*', (route) => route.fulfill({ json: initial }))
    await page.clock.install()
    await login(page)
    await expect(page.locator('tbody tr')).toHaveCount(2)
    await expect.poll(() => scanRequests).toBeGreaterThan(0)
    if (selected) {
      await page.getByRole('button', { name: '多选', exact: true }).click()
      await page.getByLabel('选择 保留.mp4', { exact: true }).check()
    }
    const previousLists = listRequests
    generation = '2'
    await page.clock.runFor(6000)
    await expect.poll(() => stateRequests).toBeGreaterThan(0)
    if (selectLate) {
      await expect.poll(() => listWaiting).toBe(true)
      await page.getByRole('button', { name: '多选', exact: true }).click()
      await page.getByLabel('选择 保留.mp4', { exact: true }).check()
      const response = page.waitForResponse((r) => r.url().includes('/api/directories/1/entries?'))
      release()
      await (await response).finished()
    }
    if (selected || selectLate) {
      await expect(page.locator('.directory-update-notice')).toContainText('目录有更新')
      expect(listRequests).toBe(previousLists + (selectLate ? 1 : 0))
      await expect(page.locator('tbody tr')).toHaveCount(2)
      await page.getByRole('button', { name: '更新文件列表', exact: true }).click()
      await expect(page.getByLabel('选择 保留.mp4', { exact: true })).toBeChecked()
      await expect(page.locator('.batch-toolbar strong')).toHaveText('已选 1 项')
    }
    await expect(page.locator('tbody tr')).toHaveCount(3)
    await expect(page.locator('.directory-update-notice')).toHaveCount(0)
    await page.getByRole('button', { name: '批次记录', exact: true }).click()
    await expect(page.getByRole('heading', { name: '批次记录.' })).toBeVisible()
    const requests = [scanRequests, stateRequests]
    await page.clock.runFor(15000)
    expect([scanRequests, stateRequests]).toEqual(requests)
    release()
  })

test('扫描失败和取消反馈清晰，不覆盖尚未保存的扫描间隔', async ({ page }) => {
  let status = {
    ...idleStatus,
    id: 'scan-test',
    status: 'scanning',
    source: 'manual',
    started: '2026-10-03T00:00:00Z',
    current_path: 'NAS/目录'
  }
  await page.route('**/api/library/scan', (route) =>
    route.fulfill({
      json: { settings: { enabled: true, interval_minutes: 60, targets: [1] }, status }
    })
  )
  await page.route('**/api/library/scan/cancel', (route) => {
    status = { ...status, status: 'cancelled' }
    return route.fulfill({ json: status })
  })
  await login(page)
  await page.getByRole('button', { name: '系统设置', exact: true }).click()
  const panel = page.locator('.library-scan-settings')
  await expect(panel).toContainText('正在扫描')
  await panel.getByLabel('扫描间隔（分钟）', { exact: true }).fill('120')
  await panel.getByRole('button', { name: '停止扫描', exact: true }).click()
  await expect(panel).toContainText('扫描已停止')
  await expect(panel.getByLabel('扫描间隔（分钟）', { exact: true })).toHaveValue('120')
  await page.route(
    '**/api/library/scan',
    (route) => route.fulfill({ status: 500, json: { message: 'NAS 连接中断' } }),
    { times: 1 }
  )
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  await expect(panel.getByRole('alert')).toContainText('NAS 连接中断')
  await panel.getByRole('button', { name: '重新读取状态', exact: true }).click()
  await expect(panel.getByRole('alert')).toHaveCount(0)
  await expect(panel.getByLabel('扫描间隔（分钟）', { exact: true })).toHaveValue('120')
})

test('扫描中长路径和大计数保持布局，范围整行且窄屏不溢出', async ({ page }) => {
  const longPath = Array(25).fill('很长的媒体文件夹名称').join(' / ')
  const status = {
    ...idleStatus,
    id: 'layout-scan',
    status: 'scanning',
    source: 'manual',
    started: new Date().toISOString(),
    current_path: longPath,
    directories: 2361,
    files: 92426,
    added: 314,
    changed: 4
  }
  await page.route('**/api/library/scan', (route) =>
    route.fulfill({
      json: { settings: { enabled: true, interval_minutes: 60, targets: [1] }, status }
    })
  )
  await login(page)
  await page.getByRole('button', { name: '系统设置', exact: true }).click()
  const panel = page.locator('.library-scan-settings')
  await panel.scrollIntoViewIfNeeded()
  await expect(panel).toContainText('正在扫描')
  const path = panel.locator('.scan-path-value')
  await expect(path).toHaveAttribute('title', longPath)
  await expect(panel.getByRole('button', { name: '停止扫描', exact: true })).toBeEnabled()
  const fields = await panel.locator('.scan-settings-fields').boundingBox()
  const scope = await panel.locator('.scan-settings-scope').boundingBox()
  expect(scope!.width).toBeGreaterThan(fields!.width * 0.9)
  await panel.screenshot({ path: 'test-results/library-scan-active-desktop.png' })
  await page.setViewportSize({ width: 390, height: 844 })
  await panel.scrollIntoViewIfNeeded()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  const pathBox = await path.boundingBox()
  expect(pathBox!.x + pathBox!.width).toBeLessThanOrEqual(390)
  await panel.screenshot({ path: 'test-results/library-scan-active-mobile.png' })
})
