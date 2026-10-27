import { defined } from './support'
import { test, expect, type Page } from '@playwright/test'

const defaults = ['命名失败', '拒绝', '无法完成', 'sorry']

async function login(page: Page) {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
  await page.goto('/#settings/prompts')
}

function removeKeyword(page: Page, keyword: string) {
  return page.getByRole('button', { name: `删除失败关键词 ${keyword}`, exact: true })
}

test('失败关键词增删、空列表、恢复默认与真实持久化', async ({ page }) => {
  await login(page)
  const input = page.getByLabel('新增失败关键词', { exact: true })
  const save = page.getByRole('button', { name: '保存失败关键词', exact: true })
  const restore = page.getByRole('button', { name: '恢复默认失败关键词', exact: true })
  await expect(page.getByRole('heading', { name: '标题失败关键词', exact: true })).toBeVisible()
  try {
    for (const keyword of defaults) await expect(removeKeyword(page, keyword)).toBeVisible()
    await expect(save).toBeDisabled()
    await expect(restore).toBeDisabled()

    await input.fill('  我不能为这个视频生成标题  ')
    await input.press('Enter')
    await expect(removeKeyword(page, '我不能为这个视频生成标题')).toBeVisible()
    await removeKeyword(page, '拒绝').click()

    // Prompt library refreshes must preserve both unsaved settings drafts.
    await page.getByLabel('固定提示词', { exact: true }).fill('未保存的固定提示词草稿')
    await page.getByRole('button', { name: '新增提示词', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '新增提示词', exact: true })
    await dialog.getByLabel('提示词名称', { exact: true }).fill('失败关键词草稿测试')
    await dialog.getByLabel('提示词内容', { exact: true }).fill('测试刷新不会丢失关键词草稿。')
    await dialog.getByRole('button', { name: '保存提示词', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    await expect(removeKeyword(page, '我不能为这个视频生成标题')).toBeVisible()
    await expect(removeKeyword(page, '拒绝')).toHaveCount(0)
    await expect(page.getByLabel('固定提示词', { exact: true })).toHaveValue(
      '未保存的固定提示词草稿'
    )
    await page.getByRole('button', { name: '删除提示词 失败关键词草稿测试', exact: true }).click()
    await page.getByRole('button', { name: '确认删除', exact: true }).click()
    await expect(removeKeyword(page, '我不能为这个视频生成标题')).toBeVisible()
    await expect(removeKeyword(page, '拒绝')).toHaveCount(0)

    await save.click()
    await expect(page.locator('.toast-message').first()).toContainText('失败关键词已保存')
    await expect(save).toBeDisabled()
    await page.reload()
    await expect(removeKeyword(page, '我不能为这个视频生成标题')).toBeVisible()
    await expect(removeKeyword(page, '拒绝')).toHaveCount(0)

    for (const keyword of ['命名失败', '无法完成', 'sorry', '我不能为这个视频生成标题']) {
      await removeKeyword(page, keyword).click()
    }
    await expect(page.getByText(/不检查失败关键词/)).toBeVisible()
    await save.click()
    await expect(save).toBeDisabled()
    await page.reload()
    await expect(page.getByText(/不检查失败关键词/)).toBeVisible()
    for (const keyword of defaults) await expect(removeKeyword(page, keyword)).toHaveCount(0)

    await restore.click()
    for (const keyword of defaults) await expect(removeKeyword(page, keyword)).toBeVisible()
    await expect(save).toBeEnabled()
    // Restore changes a draft, and reloading still returns the saved empty list.
    await page.reload()
    await expect(page.getByText(/不检查失败关键词/)).toBeVisible()
    await restore.click()
    await save.click()
    await expect(save).toBeDisabled()
    await page.reload()
    for (const keyword of defaults) await expect(removeKeyword(page, keyword)).toBeVisible()
  } finally {
    await page.goto('/#settings/prompts')
    await expect(restore).toBeVisible()
    if (await restore.isEnabled()) await restore.click()
    if (await save.isEnabled()) {
      await save.click()
      await expect(save).toBeDisabled()
    }
  }
})

for (const width of [320, 390, 1440]) {
  test(`失败关键词在 ${width}px 下支持长句、键盘及保存失败重试`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 })
    const longKeyword = 'Sorry'.repeat(40)
    let keywords = [...defaults, longKeyword]
    let failSave = true
    let putCalls = 0
    await page.route('**/api/prompts**', async (route) => {
      const request = route.request()
      if (request.method() === 'GET') {
        await route.fulfill({
          json: {
            fixed_prompt: '固定提示词测试',
            default_fixed_prompt: '固定提示词测试',
            failure_keywords: keywords,
            default_failure_keywords: defaults,
            prompts: []
          }
        })
        return
      }
      expect(new URL(request.url()).pathname).toBe('/api/prompts/failure-keywords')
      putCalls++
      if (failSave) {
        await route.fulfill({
          status: 500,
          json: {
            error: {
              code: 'internal',
              message: 'test failure',
              details: { detail: '测试保存失败，请重试' }
            }
          }
        })
      } else {
        keywords = request.postDataJSON().failure_keywords
        await route.fulfill({ json: { failure_keywords: keywords } })
      }
    })
    await login(page)
    const input = page.getByLabel('新增失败关键词', { exact: true })
    const save = page.getByRole('button', { name: '保存失败关键词', exact: true })
    await expect(removeKeyword(page, longKeyword)).toBeVisible()
    await input.fill('SoRrY')
    await input.press('Enter')
    await expect(page.getByRole('alert')).toContainText(/已存在|重复/)
    await expect(removeKeyword(page, 'SoRrY')).toHaveCount(0)
    await expect(save).toBeDisabled()
    await input.fill('句子'.repeat(101))
    await input.press('Enter')
    await expect(page.getByRole('alert')).toContainText('200')
    await input.fill('无法识别这些画面')
    await input.press('Enter')
    await expect(removeKeyword(page, '无法识别这些画面')).toBeVisible()
    await expect(input).toHaveValue('')
    await save.click()
    await expect(page.getByRole('alert')).toContainText('测试保存失败')
    await expect(removeKeyword(page, '无法识别这些画面')).toBeVisible()
    await expect(save).toBeEnabled()
    failSave = false
    await save.click()
    await expect(save).toBeDisabled()
    expect(putCalls).toBe(2)
    await page.reload()
    await expect(removeKeyword(page, '无法识别这些画面')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    const deletion = defined(await removeKeyword(page, longKeyword).boundingBox())
    expect(deletion.x).toBeGreaterThanOrEqual(0)
    expect(deletion.x + deletion.width).toBeLessThanOrEqual(width)
    if (width <= 760) {
      const footer = page.locator('.prompts-failure-card .processing-footer')
      const help = defined(await footer.locator(':scope > .help').boundingBox())
      const actions = defined(await footer.locator('.prompts-failure-actions').boundingBox())
      expect(actions.y - (help.y + help.height)).toBeLessThanOrEqual(24)
    }
    await page.screenshot({ path: `test-results/failure-keywords-${width}.png`, fullPage: true })
  })
}

test('失败关键词加载失败时禁止空草稿保存，重试后恢复', async ({ page }) => {
  let failLoad = true
  let writes = 0
  await page.route('**/api/prompts**', async (route) => {
    if (route.request().method() !== 'GET') {
      writes++
      await route.fulfill({ json: { failure_keywords: [] } })
      return
    }
    if (failLoad) {
      await route.fulfill({
        status: 500,
        json: {
          error: {
            code: 'internal',
            message: 'test failure',
            details: { detail: '测试加载失败' }
          }
        }
      })
    } else {
      await route.fulfill({
        json: {
          fixed_prompt: '固定提示词测试',
          default_fixed_prompt: '固定提示词测试',
          failure_keywords: defaults,
          default_failure_keywords: defaults,
          prompts: []
        }
      })
    }
  })
  await login(page)
  await expect(page.getByRole('alert').first()).toContainText('测试加载失败')
  await expect(page.getByLabel('新增失败关键词', { exact: true })).toHaveCount(0)
  const save = page.getByRole('button', { name: '保存失败关键词', exact: true })
  if (await save.count()) await expect(save).toBeDisabled()
  expect(writes).toBe(0)
  failLoad = false
  await page.getByRole('button', { name: '重试', exact: true }).click()
  for (const keyword of defaults) await expect(removeKeyword(page, keyword)).toBeVisible()
  await expect(save).toBeDisabled()
})
