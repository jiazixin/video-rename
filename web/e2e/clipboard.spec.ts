import { test, expect, type Page } from '@playwright/test'

// The fixture still listens on loopback; this hostname is an actual insecure HTTP origin.
test.use({
  launchOptions: {
    args: ['--host-resolver-rules=MAP clipboard-http.test 127.0.0.1', '--proxy-server=direct://']
  }
})

const responseText = JSON.stringify({
  choices: [{ message: { content: '测试响应：可复制的中文诊断' } }]
})
const extractionText = '抽帧现场：ffmpeg 退出码 7，采样点 1.25s'
const numberingMessage = '没有需要编号的文件'
const numberingText = '编号现场：原文件 photo.jpg，目标 1.jpg，类型 directory'
const historyItem = {
  id: 9301,
  category: 'success',
  action: 'manual',
  old_path: '复制测试/原名.mp4',
  new_path: '复制测试/新名.mp4',
  created: '2026-10-02T00:00:00Z'
}
const extractionItem = {
  id: 9302,
  run: 0,
  entry: 1,
  path: '复制测试/broken.mp4',
  source: 'manual',
  status: 'failure',
  started: '2026-10-02T00:00:00Z',
  finished: '2026-10-02T00:00:01Z',
  duration_ms: 1000,
  cached: false,
  frames: 0,
  error: { code: 'extract_failed' }
}

async function mockPages(page: Page) {
  await page.route('**/api/logs?*', (route) =>
    route.fulfill({ json: { items: [historyItem], total: 1, page: 1 } })
  )
  await page.route('**/api/logs/batches*', (route) => route.fulfill({ json: { items: [] } }))
  await page.route('**/api/logs/9301/diagnostics', (route) =>
    route.fulfill({
      json: { request: '{"model":"clipboard-fixture"}', response: responseText, processing: '' }
    })
  )
  await page.route('**/api/extractions?*', (route) =>
    route.fulfill({
      json: {
        success: { items: [], total: 0, next: 0, page: 1 },
        failure: { items: [extractionItem], total: 1, next: 0, page: 1 },
        running: 0
      }
    })
  )
  await page.route('**/api/extractions/9302', (route) =>
    route.fulfill({ json: { ...extractionItem, processing_log: extractionText } })
  )
  await page.route('**/api/numbering/jobs', (route) =>
    route.fulfill({
      status: 409,
      json: {
        error: {
          code: 'nothing_to_number',
          message: 'no files need numbering',
          details: { processing_log: numberingText }
        }
      }
    })
  )
  await page.route('**/api/numbering/jobs/*/cancel', (route) => route.fulfill({ json: {} }))
}

async function login(page: Page, url = '/') {
  await page.goto(url)
  await page.getByLabel('用户名', { exact: true }).fill('admin')
  await page.getByLabel('密码', { exact: true }).fill('test-password-123')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '文件目录.' })).toBeVisible()
}

async function installClipboard(page: Page, mode: 'modern' | 'reject' | 'http' | 'deferred') {
  await page.addInitScript((mode) => {
    const state = {
      modern: [] as string[],
      fallback: [] as string[],
      fallbackAllowed: true,
      reject: undefined as (() => void) | undefined
    }
    window.clipboardTest = state
    // The app falls back to execCommand on insecure origins; observe that path.
    // eslint-disable-next-line @typescript-eslint/no-deprecated -- the fallback under test
    const nativeCopy = document.execCommand.bind(document)
    // eslint-disable-next-line @typescript-eslint/no-deprecated -- the fallback under test
    document.execCommand = (command, ...args) => {
      if (command === 'copy') {
        state.fallback.push(window.getSelection()?.toString() ?? '')
        if (!state.fallbackAllowed) return false
      }
      // eslint-disable-next-line @typescript-eslint/no-deprecated -- the fallback under test
      return nativeCopy(command, ...args)
    }
    if (mode !== 'http') {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: async (text: string) => {
            state.modern.push(text)
            if (mode === 'reject') throw new DOMException('Permission denied', 'NotAllowedError')
            if (mode === 'deferred')
              await new Promise<void>((_, reject) => {
                state.reject = () => {
                  reject(new DOMException('Permission denied', 'NotAllowedError'))
                }
              })
          }
        }
      })
    }
  }, mode)
}

async function openResponse(page: Page) {
  await page.getByRole('button', { name: '运行记录', exact: true }).click()
  const diagnostic = page.locator('.history-table .failure-diagnostics').first()
  await diagnostic.getByRole('button', { name: '原始响应', exact: true }).click()
  await expect(diagnostic.locator('pre')).toHaveText(responseText)
  return diagnostic
}

async function copyAllEntrances(page: Page) {
  await page.getByRole('button', { name: '运行记录', exact: true }).click()
  await page.getByRole('button', { name: '详情', exact: true }).click()
  const history = page.getByRole('dialog', { name: '运行记录详情', exact: true })
  await history.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(page.locator('.toast-message')).toContainText(['已复制运行记录详情'])
  await history.getByRole('button', { name: '关闭', exact: true }).click()

  const diagnostic = await openResponse(page)
  await diagnostic.getByRole('button', { name: '复制', exact: true }).click()
  await expect(diagnostic.getByRole('button', { name: '已复制', exact: true })).toBeVisible()

  await page.getByRole('button', { name: '抽帧记录', exact: true }).click()
  await page.getByRole('button', { name: '失败记录', exact: true }).click()
  await page.getByRole('button', { name: '详细原因', exact: true }).click()
  const extraction = page.getByRole('dialog', { name: '抽帧失败详情', exact: true })
  await expect(extraction.locator('pre')).toHaveText(extractionText)
  await extraction.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(page.locator('.toast-message')).toContainText(['已复制抽帧详情'])
  await extraction.getByRole('button', { name: '关闭', exact: true }).last().click()

  await page.getByRole('button', { name: '文件目录', exact: true }).click()
  await page
    .locator('.directory-mark-actions')
    .getByRole('button', { name: '编号', exact: true })
    .click()
  const numbering = page.getByRole('dialog')
  await numbering.getByText('查看诊断详情', { exact: true }).click()
  await numbering.getByRole('button', { name: '复制诊断', exact: true }).click()
  await expect(numbering.locator('.numbering-diagnostics [role=status]')).toHaveText('已复制诊断')
  await expect(page.locator('textarea')).toHaveCount(0)
}

const expectedCopies = [
  JSON.stringify(historyItem, null, 2),
  responseText,
  JSON.stringify({ ...extractionItem, processing_log: extractionText }, null, 2),
  `${numberingMessage}\n${numberingText}`
]

const clipboardModes = {
  modern: '现代剪贴板',
  http: 'HTTP 局域网无剪贴板 API',
  reject: '现代 API 拒绝后的降级'
} as const

for (const mode of ['modern', 'http', 'reject'] as const) {
  test(`${clipboardModes[mode]}复制全部四种入口`, async ({ page }) => {
    await mockPages(page)
    await installClipboard(page, mode)
    await login(page, mode === 'http' ? 'http://clipboard-http.test:18790/' : '/')
    if (mode === 'http')
      expect(
        await page.evaluate(() => ({
          secure: window.isSecureContext,
          clipboard: typeof navigator.clipboard
        }))
      ).toEqual({ secure: false, clipboard: 'undefined' })
    await copyAllEntrances(page)
    const state = await page.evaluate(() => window.clipboardTest)
    expect(state.modern).toEqual(mode === 'http' ? [] : expectedCopies)
    expect(state.fallback).toEqual(mode === 'modern' ? [] : expectedCopies)
  })
}

test('两个复制途径均失败保留诊断原文，清理选区并支持重试', async ({ page }) => {
  await mockPages(page)
  await installClipboard(page, 'reject')
  await login(page)
  const diagnostic = await openResponse(page)
  await page.evaluate(() => {
    window.clipboardTest.fallbackAllowed = false
  })
  await diagnostic.getByRole('button', { name: '复制', exact: true }).click()
  await expect(diagnostic.getByRole('alert')).toHaveText('复制失败，请手动选取文本')
  await expect(diagnostic.locator('pre')).toHaveText(responseText)
  await expect(diagnostic.getByRole('button', { name: '已复制', exact: true })).toHaveCount(0)
  await expect(page.locator('textarea')).toHaveCount(0)
  await page.evaluate(() => {
    window.clipboardTest.fallbackAllowed = true
  })
  await diagnostic.getByRole('button', { name: '复制', exact: true }).click()
  await expect(diagnostic.getByRole('button', { name: '已复制', exact: true })).toBeVisible()
  await expect(diagnostic.getByRole('alert')).toHaveCount(0)
})

test('降级复制恢复焦点、输入选区、文本选区与滚动位置', async ({ page }) => {
  await mockPages(page)
  await installClipboard(page, 'http')
  await login(page, 'http://clipboard-http.test:18790/')
  const diagnostic = await openResponse(page)
  const button = diagnostic.locator('.diagnostic-content header button')
  await button.focus()
  const selectedText = await diagnostic.locator('pre').evaluate((element) => {
    const range = document.createRange()
    const text = element.firstChild
    const selection = window.getSelection()
    if (!text || !selection) throw new Error('missing text or selection')
    range.setStart(text, 3)
    range.setEnd(text, 13)
    selection.removeAllRanges()
    selection.addRange(range)
    return selection.toString()
  })
  await button.evaluate((element) => {
    ;(element as HTMLButtonElement).click()
  })
  await expect(button).toBeFocused()
  expect(await page.evaluate(() => window.getSelection()?.toString())).toBe(selectedText)
  await expect(page.locator('textarea')).toHaveCount(0)

  await page.evaluate(() => {
    const input = document.createElement('input')
    input.id = 'clipboard-focus-fixture'
    input.value = '需要保持的输入选区'.repeat(50)
    input.style.cssText = 'position:fixed;top:0;left:0;width:100px'
    const space = document.createElement('div')
    space.style.height = '2000px'
    document.body.append(input, space)
    input.focus({ preventScroll: true })
    input.setSelectionRange(2, 8, 'backward')
    input.scrollLeft = 40
    window.scrollTo(0, 100)
  })
  const before = await page
    .locator('#clipboard-focus-fixture')
    .evaluate((element) => ({ left: element.scrollLeft, scrollY: window.scrollY }))
  await button.evaluate((element) => {
    ;(element as HTMLButtonElement).click()
  })
  await expect(page.locator('#clipboard-focus-fixture')).toBeFocused()
  expect(
    await page.locator('#clipboard-focus-fixture').evaluate((element) => {
      const input = element as HTMLInputElement
      return {
        start: input.selectionStart,
        end: input.selectionEnd,
        direction: input.selectionDirection,
        left: input.scrollLeft,
        scrollY: window.scrollY
      }
    })
  ).toEqual({ start: 2, end: 8, direction: 'backward', ...before })
  await expect(page.locator('textarea')).toHaveCount(0)
})

test('等待现代复制时阻止重复，迟到拒绝后降级保留当前焦点', async ({ page }) => {
  await mockPages(page)
  await installClipboard(page, 'deferred')
  await login(page)
  const diagnostic = await openResponse(page)
  const button = diagnostic.getByRole('button', { name: '复制', exact: true })
  await button.click()
  await expect(button).toBeDisabled()
  await page.getByLabel('搜索运行记录').focus()
  await page.evaluate(() => {
    window.clipboardTest.reject?.()
  })
  await expect(diagnostic.getByRole('button', { name: '已复制', exact: true })).toBeVisible()
  await expect(page.getByLabel('搜索运行记录')).toBeFocused()
  expect(
    await page.evaluate(() => ({
      modern: window.clipboardTest.modern.length,
      fallback: window.clipboardTest.fallback.length
    }))
  ).toEqual({ modern: 1, fallback: 1 })
  await expect(page.locator('textarea')).toHaveCount(0)
})

test('不同入口同时复制不会让迟到的降级覆盖新请求', async ({ page }) => {
  await mockPages(page)
  await installClipboard(page, 'deferred')
  await login(page)
  const diagnostic = await openResponse(page)
  await diagnostic.getByRole('button', { name: '复制', exact: true }).click()
  await expect(diagnostic.getByRole('button', { name: '复制', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '详情', exact: true }).click()
  const modal = page.getByRole('dialog', { name: '运行记录详情', exact: true })
  await modal.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(modal.getByRole('alert')).toHaveText('正在复制，请稍后重试')
  expect(await page.evaluate(() => window.clipboardTest.modern)).toEqual([responseText])
  await page.evaluate(() => {
    window.clipboardTest.reject?.()
  })
  await expect(diagnostic.getByRole('button', { name: '已复制', exact: true })).toBeVisible()
  await page.evaluate(() => {
    navigator.clipboard.writeText = (text) => {
      window.clipboardTest.modern.push(text)
      return Promise.resolve()
    }
  })
  await modal.getByRole('button', { name: '复制详情', exact: true }).click()
  await expect(modal.getByRole('alert')).toHaveCount(0)
  await expect(page.locator('.toast-message')).toContainText(['已复制运行记录详情'])
  expect(await page.evaluate(() => window.clipboardTest.modern)).toEqual([
    responseText,
    JSON.stringify(historyItem, null, 2)
  ])
  await expect(page.locator('textarea')).toHaveCount(0)
})
