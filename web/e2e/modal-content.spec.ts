import { defined } from './support'
import { test, expect } from '@playwright/test'

const longTitle = 'AbCd'.repeat(20)
const created = '2026-10-01T00:00:00Z'
const prompt = {
  id: 100,
  title: '测试提示词',
  body: '测试正文',
  version: 1,
  created,
  updated: created
}
const model = {
  id: 100,
  title: longTitle,
  prompt_id: prompt.id,
  prompt_title: prompt.title,
  body: prompt.body,
  version: 1,
  created,
  updated: created,
  is_default: false,
  ready: true,
  workers: 2,
  thinking: 'default',
  thinking_adapter: 'auto',
  thinking_effort: 'low',
  thinking_budget: 1024,
  provider: 'custom',
  protocol: 'openai',
  base_url: 'http://127.0.0.1:8080/v1',
  model: 'vision-test',
  timeout: '180s',
  temperature: 0.7,
  max_tokens: 1024,
  omit_temperature: false,
  has_api_key: false
}
const plan = {
  id: 100,
  name: longTitle,
  targets: [1],
  target_names: ['媒体库'],
  recursive: true,
  kind: 'daily',
  expression: '0 2 * * *',
  timezone: 'Asia/Shanghai',
  enabled: false,
  next: '',
  last: '',
  version: 1,
  model_config_id: model.id,
  task: 'rename'
}

for (const width of [320, 390, 1440]) {
  test(`模型和计划删除确认在 ${width}px 下完整显示最长英文标题`, async ({ page }) => {
    expect(longTitle.length).toBe(80)
    await page.setViewportSize({ width, height: 1000 })
    await page.route('**/api/**', (route) => {
      const request = route.request(),
        path = new URL(request.url()).pathname
      if (
        request.method() !== 'GET' &&
        !(request.method() === 'POST' && path === '/api/auth/login')
      ) {
        return route.fulfill({ json: {} })
      }
      if (path === '/api/model-configs') return route.fulfill({ json: { items: [model] } })
      if (path === '/api/prompts')
        return route.fulfill({ json: { prompts: [prompt], fixed_prompt: '测试固定提示词' } })
      if (path === '/api/schedules') return route.fulfill({ json: { items: [plan] } })
      return route.continue()
    })
    await page.goto('/')
    await page.getByLabel('用户名', { exact: true }).fill('admin')
    await page.getByLabel('密码', { exact: true }).fill('test-password-123')
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
    for (const item of [
      {
        kind: 'model',
        hash: 'settings/models',
        button: `删除模型配置 ${longTitle}`,
        dialog: '删除模型配置'
      },
      { kind: 'plan', hash: 'plans', button: `删除 ${longTitle}`, dialog: '删除运行计划' }
    ]) {
      await page.goto(`/#${item.hash}`)
      await page.getByRole('button', { name: item.button, exact: true }).click()
      const dialog = page.getByRole('dialog', { name: item.dialog, exact: true })
      await expect(dialog).toContainText(longTitle)
      const content = dialog.locator('.modal-content')
      await expect.poll(() => content.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
      const layout = await content.evaluate((el) => {
        const bounds = el.getBoundingClientRect(),
          range = document.createRange()
        const paragraph = el.querySelector('p')
        if (paragraph) range.selectNodeContents(paragraph)
        return {
          left: bounds.left,
          right: bounds.right,
          text: [...range.getClientRects()].map((r) => ({ left: r.left, right: r.right }))
        }
      })
      for (const bounds of layout.text) {
        expect(bounds.left).toBeGreaterThanOrEqual(layout.left)
        expect(bounds.right).toBeLessThanOrEqual(layout.right)
      }
      const bounds = defined(await dialog.boundingBox())
      expect(bounds.x).toBeGreaterThanOrEqual(0)
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(width)
      await expect(dialog.getByRole('button', { name: '取消', exact: true })).toBeVisible()
      await page.screenshot({
        path: `test-results/modal-delete-${item.kind}-${width}.png`,
        animations: 'disabled'
      })
      await dialog.getByRole('button', { name: '取消', exact: true }).click()
      await expect(dialog).toHaveCount(0)
    }
  })
}
