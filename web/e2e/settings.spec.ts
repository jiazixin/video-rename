import { defined, toasts } from './support'
import { test, expect } from '@playwright/test'
test('系统设置聚合、处理参数持久化与窄屏', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '系统设置', exact: true }).click()
  await expect(page.getByRole('heading', { name: '系统设置.' })).toBeVisible()
  await expect(page.getByLabel('同时运行批次上限', { exact: true })).toHaveValue('32')
  await expect(page.getByLabel('等待批次上限', { exact: true })).toHaveValue('1000')
  await expect(page.getByLabel('同时抽帧的视频数', { exact: true })).toHaveValue('8')
  await expect(page.getByLabel('临时目录容量（GB）', { exact: true })).toHaveValue('10')
  await expect(page.getByLabel('稳定性等待时长（秒）', { exact: true })).toHaveValue('30')
  await page.getByLabel('同时运行批次上限', { exact: true }).fill('64')
  await page.getByLabel('等待批次上限', { exact: true }).fill('5000')
  await page.getByLabel('稳定性等待时长（秒）', { exact: true }).fill('0')
  await page.getByLabel('同时抽帧的视频数', { exact: true }).fill('3')
  await page.getByLabel('临时目录容量（GB）', { exact: true }).fill('2')
  await page.getByRole('button', { name: '保存系统设置' }).click()
  await expect(toasts(page).first()).toContainText('设置已保存')
  await expect(page.getByRole('main').getByText('设置已保存')).toHaveCount(0)
  await expect(page.locator('.toast-stack').filter({ has: toasts(page) })).toHaveCSS(
    'position',
    'fixed'
  )
  await page.reload()
  await expect(page.getByLabel('同时运行批次上限', { exact: true })).toHaveValue('64')
  await expect(page.getByLabel('等待批次上限', { exact: true })).toHaveValue('5000')
  await expect(page.getByLabel('同时抽帧的视频数', { exact: true })).toHaveValue('3')
  await expect(page.getByLabel('临时目录容量（GB）', { exact: true })).toHaveValue('2')
  await expect(page.getByLabel('稳定性等待时长（秒）', { exact: true })).toHaveValue('0')
  for (const name of ['后台目录扫描', 'Prompt配置', '模型配置', '抽帧设置', '通知设置']) {
    await page.getByRole('button', { name, exact: true }).click()
    await expect(page.getByRole('heading', { name: name + '.' })).toHaveCount(0)
    await expect(page.getByRole('button', { name, exact: true })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    await page.setViewportSize({ width: 1920, height: 1080 })
    await expect(page.getByRole('main').getByRole('heading').first()).toBeVisible()
    if (name === '模型配置') {
      const card = page.getByRole('article').first()
      const endpoint = card.locator('.model-endpoint')
      const before = await endpoint.boundingBox()
      await card.getByText(/^命名提示词 · /).click()
      await expect(card.getByRole('group').getByRole('paragraph')).toBeVisible()
      const after = await endpoint.boundingBox()
      expect(Math.abs(defined(before).height - defined(after).height)).toBeLessThan(2)
    }
    if (name === '抽帧设置') {
      const tabs = await page.getByRole('group', { name: '系统设置分类' }).boundingBox()
      const content = await page.locator('.sampling-page').boundingBox()
      expect(Math.abs(defined(tabs).width - defined(content).width)).toBeLessThan(2)
    }
    await page.screenshot({ path: `test-results/settings-${name}-desktop.png`, fullPage: true })
    await page.setViewportSize({ width: 390, height: 844 })
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true)
    await page.screenshot({ path: `test-results/settings-${name}-mobile.png`, fullPage: true })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.reload()
    await expect(page.getByRole('heading', { name: name + '.' })).toHaveCount(0)
    await expect(page.getByRole('button', { name, exact: true })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
  }
  await page.getByRole('button', { name: '处理与缓存', exact: true }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByText('连续 3 天未使用的截图自动删除。', { exact: false })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  )
  await page.screenshot({ path: 'test-results/system-settings-mobile.png', fullPage: true })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: 'test-results/system-settings-desktop.png', fullPage: true })
  await page.getByLabel('同时运行批次上限', { exact: true }).fill('32')
  await page.getByLabel('等待批次上限', { exact: true }).fill('1000')
  await page.getByLabel('稳定性等待时长（秒）', { exact: true }).fill('30')
  await page.getByLabel('同时抽帧的视频数', { exact: true }).fill('8')
  await page.getByLabel('临时目录容量（GB）', { exact: true }).fill('10')
  await page.getByRole('button', { name: '保存系统设置' }).click()
  await expect(toasts(page).first()).toContainText('设置已保存')
})

test('重试与缓存保留设置、清空确认和失败反馈', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('button', { name: '系统设置', exact: true }).click()
  await expect(page.getByLabel('开启失败自动重试', { exact: true })).toBeChecked()
  await expect(page.getByLabel('最大重试次数', { exact: true })).toHaveValue('3')
  await page.getByLabel('最大重试次数', { exact: true }).fill('2')
  await page.getByLabel('延时删除', { exact: true }).check()
  await page.getByLabel('成功后保留时长', { exact: true }).fill('7')
  await page.getByLabel('按闲置时间自动清理', { exact: true }).uncheck()
  await page.getByRole('button', { name: '保存系统设置' }).click()
  await expect(toasts(page).first()).toContainText('设置已保存')
  await page.reload()
  await expect(page.getByLabel('最大重试次数', { exact: true })).toHaveValue('2')
  await expect(page.getByLabel('成功后保留时长', { exact: true })).toHaveValue('7')
  await expect(page.getByLabel('按闲置时间自动清理', { exact: true })).not.toBeChecked()
  await expect(page.getByRole('region', { name: '缓存状态' })).toContainText(
    '成功改名后保留 7 天。不按闲置时间自动删除。'
  )
  const clear = page.getByRole('button', { name: '清空缓存', exact: true })
  await expect(clear).toHaveCSS('background-color', 'rgb(255, 240, 240)')
  const buttons = await page
    .getByRole('region', { name: '缓存状态' })
    .getByRole('button')
    .allTextContents()
  expect(buttons).toEqual(['清空缓存', '刷新状态'])
  let calls = 0
  page.on('request', (request) => {
    if (request.url().endsWith('/api/system/cache/clear')) calls++
  })
  await clear.click()
  const dialog = page.getByRole('dialog', { name: '确认清空缓存？' })
  await expect(dialog).toContainText('正在使用或正在抽帧的缓存会保留')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  expect(calls).toBe(0)
  await page.route(
    '**/api/system/cache/clear',
    (route) =>
      route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({
          error: {
            code: 'internal',
            message: 'test failure',
            details: { detail: '清理失败测试' }
          }
        })
      }),
    { times: 1 }
  )
  await clear.click()
  await dialog.getByRole('button', { name: '清空缓存', exact: true }).click()
  await expect(dialog.getByRole('alert')).toContainText('服务暂时无法完成请求')
  await dialog.getByRole('button', { name: '清空缓存', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(toasts(page).first()).toContainText('已清理')
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  )
  await page.screenshot({ path: 'test-results/retry-cache-mobile.png', fullPage: true })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: 'test-results/retry-cache-desktop.png', fullPage: true })
  await page.getByLabel('最大重试次数', { exact: true }).fill('3')
  await page.getByLabel('立即删除（默认）', { exact: true }).check()
  await page.getByLabel('按闲置时间自动清理', { exact: true }).check()
  await page.getByRole('button', { name: '保存系统设置' }).click()
  await expect(toasts(page).first()).toContainText('设置已保存')
})
