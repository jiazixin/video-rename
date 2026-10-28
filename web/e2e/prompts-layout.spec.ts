import { defined } from './support'
import { test, expect, type Page } from '@playwright/test'

const ordinaryTitle = '旅行视频标题'
const longTitle = '长提示词标题'.repeat(13) + '结束'
const longAsciiTitle = 'AbCd'.repeat(20)
const fixedPrompt = '固定输出约束：只输出一行标题。'
const fixedDraft = '尚未保存的固定提示词草稿'

async function mockPrompts(page: Page) {
  let rows = [ordinaryTitle, longTitle, longAsciiTitle].map((title, i) => ({
    id: 100 + i,
    title,
    body: `用于测试 ${title} 的提示词正文。`,
    etag: '"v1"',
    version: 1,
    created: '2026-10-01T00:00:00.000Z',
    updated: '2026-10-01T00:00:00.000Z'
  }))
  await page.route('**/api/prompts**', async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/api/prompts' && request.method() === 'GET') {
      await route.fulfill({
        json: {
          fixed_prompt: fixedPrompt,
          default_fixed_prompt: fixedPrompt,
          failure_keywords: ['命名失败', '拒绝', '无法完成', 'sorry'],
          default_failure_keywords: ['命名失败', '拒绝', '无法完成', 'sorry'],
          prompts: rows
        }
      })
      return
    }
    const row = rows.find((p) => path === `/api/prompts/${p.id}`)
    expect(row).toBeDefined()
    if (!row) throw new Error(`Unexpected prompt request: ${request.method()} ${path}`)
    const body = request.postDataJSON()
    expect(request.headers()['if-match']).toBe(row.etag)
    expect(body).not.toHaveProperty('version')
    if (request.method() === 'PATCH') {
      Object.assign(row, {
        title: body.title,
        body: body.body,
        version: row.version + 1,
        etag: `"v${row.version + 1}"`
      })
      await route.fulfill({ json: row })
    } else {
      expect(request.method()).toBe('DELETE')
      rows = rows.filter((p) => p.id !== row.id)
      await route.fulfill({ json: {} })
    }
  })
}

async function login(page: Page) {
  await page.goto('/')
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
  await page.goto('/settings/prompts')
  await expect(page.getByRole('heading', { name: '提示词库', exact: true })).toBeVisible()
}

for (const width of [320, 390, 1440]) {
  test(`Prompt 卡片在 ${width}px 下保持标题完整与操作可用`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 })
    await mockPrompts(page)
    await login(page)
    expect(longTitle.length).toBe(80)
    expect(longAsciiTitle.length).toBe(80)
    const fixedLayout = await page.locator('.prompts-fixed-card').evaluate((element) => {
      const bounds = (node: Element) => {
        const rect = node.getBoundingClientRect()
        return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom }
      }
      const textBounds: { left: number; right: number }[] = []
      const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT)
      while (walker.nextNode()) {
        if (!walker.currentNode.textContent?.trim()) continue
        const range = document.createRange()
        range.selectNodeContents(walker.currentNode)
        for (const rect of range.getClientRects()) {
          textBounds.push({ left: rect.left, right: rect.right })
        }
      }
      return {
        card: bounds(element),
        controls: [...element.querySelectorAll('textarea, .prompts-fixed-actions button')].map(
          bounds
        ),
        textBounds
      }
    })
    for (const bounds of fixedLayout.controls) {
      expect(bounds.left).toBeGreaterThanOrEqual(fixedLayout.card.left + 1)
      expect(bounds.right).toBeLessThanOrEqual(fixedLayout.card.right - 1)
      expect(bounds.top).toBeGreaterThanOrEqual(fixedLayout.card.top + 1)
      expect(bounds.bottom).toBeLessThanOrEqual(fixedLayout.card.bottom - 1)
    }
    for (const bounds of fixedLayout.textBounds) {
      expect(bounds.left).toBeGreaterThanOrEqual(fixedLayout.card.left + 1)
      expect(bounds.right).toBeLessThanOrEqual(fixedLayout.card.right - 1)
    }
    for (const title of [ordinaryTitle, longTitle, longAsciiTitle]) {
      const card = page.getByRole('article', { name: title, exact: true })
      await expect(card).toBeVisible()
      const layout = await card.evaluate((element) => {
        const bounds = (selector: string) => {
          const rect = element.querySelector(selector)?.getBoundingClientRect()
          return {
            left: rect?.left ?? NaN,
            right: rect?.right ?? NaN,
            top: rect?.top ?? NaN,
            bottom: rect?.bottom ?? NaN,
            width: rect?.width ?? NaN
          }
        }
        const heading = element.querySelector('.prompt-card-heading')
        return {
          header: bounds('header'),
          heading: bounds('.prompt-card-heading'),
          title: bounds('h2'),
          actions: bounds('.prompt-card-actions'),
          edit: bounds('.prompt-card-actions button:first-child'),
          remove: bounds('.prompt-card-actions button:last-child'),
          overflow: heading ? heading.scrollWidth > heading.clientWidth : true
        }
      })
      expect(layout.overflow).toBe(false)
      if (width <= 760) {
        expect(Math.abs(layout.title.width - layout.header.width)).toBeLessThan(1)
        expect(layout.actions.top).toBeGreaterThanOrEqual(layout.heading.bottom + 11)
        expect(Math.abs(layout.actions.right - layout.header.right)).toBeLessThan(1)
        expect(layout.edit.left).toBeGreaterThanOrEqual(layout.header.left - 1)
        expect(Math.abs(layout.edit.top - layout.remove.top)).toBeLessThan(1)
      } else {
        expect(Math.abs(layout.actions.top - layout.heading.top)).toBeLessThan(1)
        expect(layout.heading.right + 15).toBeLessThanOrEqual(layout.actions.left)
      }
      await expect(
        card.getByRole('button', { name: `编辑提示词 ${title}`, exact: true })
      ).toBeVisible()
      await expect(
        card.getByRole('button', { name: `删除提示词 ${title}`, exact: true })
      ).toBeVisible()
    }
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true)
    await page.screenshot({ path: `test-results/prompts-layout-${width}.png`, fullPage: true })

    const fixed = page.getByLabel('固定提示词', { exact: true })
    await fixed.fill(fixedDraft)
    await page.getByRole('button', { name: `编辑提示词 ${longTitle}`, exact: true }).click()
    const editor = page.getByRole('dialog', { name: '编辑提示词', exact: true })
    await expect(editor.getByLabel('提示词名称', { exact: true })).toHaveValue(longTitle)
    await page.screenshot({
      path: `test-results/prompts-editor-${width}.png`,
      animations: 'disabled'
    })
    const editorBounds = defined(await editor.boundingBox())
    expect(editorBounds.x).toBeGreaterThanOrEqual(0)
    expect(editorBounds.x + editorBounds.width).toBeLessThanOrEqual(width)
    await editor.getByLabel('提示词内容', { exact: true }).fill('更新后的提示词正文。')
    await editor.getByRole('button', { name: '保存提示词', exact: true }).click()
    await expect(editor).toHaveCount(0)
    await expect(page.getByRole('article', { name: longTitle })).toContainText(
      '更新后的提示词正文。'
    )
    await expect(fixed).toHaveValue(fixedDraft)
    for (const title of [longTitle, longAsciiTitle]) {
      await page.getByRole('button', { name: `删除提示词 ${title}`, exact: true }).click()
      const deletion = page.getByRole('dialog', { name: '删除提示词', exact: true })
      await expect(deletion).toContainText(title)
      const suffix = title === longAsciiTitle ? '-ascii' : ''
      await page.screenshot({
        path: `test-results/prompts-delete${suffix}-${width}.png`,
        animations: 'disabled'
      })
      const deletionBounds = defined(await deletion.boundingBox())
      expect(deletionBounds.x).toBeGreaterThanOrEqual(0)
      expect(deletionBounds.x + deletionBounds.width).toBeLessThanOrEqual(width)
      const content = await deletion.locator('.modal-content').evaluate((element) => ({
        width: element.clientWidth,
        scrollWidth: element.scrollWidth
      }))
      expect(content.scrollWidth).toBeLessThanOrEqual(content.width)
      await deletion.getByRole('button', { name: '确认删除', exact: true }).click()
      await expect(deletion).toHaveCount(0)
      await expect(page.getByRole('heading', { name: title, exact: true })).toHaveCount(0)
      await expect(fixed).toHaveValue(fixedDraft)
    }
    await expect(page.getByRole('heading', { name: ordinaryTitle, exact: true })).toBeVisible()
    await expect(fixed).toHaveValue(fixedDraft)
  })
}
